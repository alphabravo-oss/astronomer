package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// UserQuerier abstracts the user-related database queries needed by AuthHandler.
// This allows for easy testing with mock implementations.
type UserQuerier interface {
	GetUserByEmail(ctx context.Context, email string) (sqlc.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	UpdateUserLastLogin(ctx context.Context, id uuid.UUID) error
}

// LockoutQuerier is the optional dependency that backs the account-
// lockout policy. When unwired (the test-fake path), Login behaves
// exactly as before — no per-account state is tracked. When attached
// from production, Login increments the failed-attempt counter, locks
// the account after the threshold, and resets the counter on success.
type LockoutQuerier interface {
	IncrementFailedLoginCount(ctx context.Context, arg sqlc.IncrementFailedLoginCountParams) error
	ResetFailedLoginCount(ctx context.Context, id uuid.UUID) error
	LockUser(ctx context.Context, arg sqlc.LockUserParams) error
	UnlockUser(ctx context.Context, id uuid.UUID) error
}

// RevocationQuerier backs the JWT revocation list + per-user invalidation
// cutoff. Wired separately from UserQuerier so test fakes can opt in
// piece-by-piece.
type RevocationQuerier interface {
	RevokeJWT(ctx context.Context, arg sqlc.RevokeJWTParams) error
	InvalidateAllTokens(ctx context.Context, arg sqlc.InvalidateAllTokensParams) error
}

// SSOSessionStore is the narrow surface AuthHandler.Logout consults to
// drive RP-initiated single sign-out (migration 054). Wired separately
// so tests can opt in incrementally; nil disables SLO and Logout
// degrades to "JWT revoked locally only" (the pre-054 behaviour).
type SSOSessionStore interface {
	GetSSOSession(ctx context.Context, jti string) (sqlc.SsoSession, error)
	DeleteSSOSession(ctx context.Context, jti string) error
}

// AuthAuditWriter is the optional audit-writer dependency for AuthHandler.
// Wired separately because UserQuerier is used by tests with narrow fakes
// that do not (and should not) implement the audit writer. Auth-handler is
// also unique in that it must record a user_id on success but accept an
// anonymous (NULL) user_id on failed login.
type AuthAuditWriter interface {
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
}

// PasswordRehasher updates a user's password column. It is satisfied by the
// generated sqlc Queries type via UpdateUserPasswordHash and is used by the
// login handler to opportunistically migrate Django-format hashes to bcrypt.
// It also clears the bootstrap must_change_password flag after a successful
// password change via the dashboard.
type PasswordRehasher interface {
	UpdateUserPasswordHash(ctx context.Context, arg sqlc.UpdateUserPasswordHashParams) error
	ClearMustChangePassword(ctx context.Context, id uuid.UUID) error
}

// RoleBindingsQuerier supplies the aggregated role bindings rendered into
// /api/v1/auth/me/. It is implemented by the generated sqlc Queries type.
//
// Was 6 methods (3 ListBindings + 3 GetRoleByID) feeding an N+1 fan-out in
// collectRoles. Collapsed to a single UNION-ALL query that returns one row
// per binding with the role pre-joined; the frontend polls /auth/me on every
// page nav so any extra round-trip here hurts.
type RoleBindingsQuerier interface {
	ListUserBindingsWithRoles(ctx context.Context, userID pgtype.UUID) ([]sqlc.ListUserBindingsWithRolesRow, error)
}

// TokenQuerier abstracts the API token database queries needed by AuthHandler.
type TokenQuerier interface {
	CreateAPIToken(ctx context.Context, arg sqlc.CreateAPITokenParams) (sqlc.ApiToken, error)
	ListTokensByUser(ctx context.Context, arg sqlc.ListTokensByUserParams) ([]sqlc.ApiToken, error)
	CountTokensByUser(ctx context.Context, userID uuid.UUID) (int64, error)
	GetAPITokenByID(ctx context.Context, id uuid.UUID) (sqlc.ApiToken, error)
	RevokeAPIToken(ctx context.Context, id uuid.UUID) error
}

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	queries     UserQuerier
	tokens      TokenQuerier
	rehasher    PasswordRehasher
	roles       RoleBindingsQuerier
	audit       AuthAuditWriter
	lockout     LockoutQuerier
	revocation  RevocationQuerier
	jwt         *auth.JWTManager
	log         *slog.Logger
	lockoutDur  time.Duration
	failThresh  int

	// totpGate is the optional 2FA enrollment lookup. When wired,
	// Login switches to the challenge-token flow whenever the user
	// has a row in user_totp_enrollments; without it, the legacy
	// password-only path stands. Separate from totpRequireAll so
	// the test fakes can attach the gate without flipping the
	// global enforcement bit.
	totpGate        TOTPEnrollmentGate
	totpRequireAll  bool

	// emails is the optional email-enqueue hook used by the
	// lockout + token-created hot paths. Optional and best-effort:
	// a missing SMTP relay must not fail a user-facing action.
	emails EmailNotifier
	// passwordResets is the optional password-reset token store. When
	// nil, the /auth/password-reset/* endpoints respond 503. Decoupled
	// from emails so a test fake can wire one without the other.
	passwordResets PasswordResetStore
	// enforcer guards CreateToken against the per-user
	// max_tokens_per_user quota (migration 051). Optional; nil
	// disables the check.
	enforcer *quota.Enforcer

	// ssoSessions persists / reads the sso_sessions table used by the
	// single sign-out flow (migration 054). Optional — when nil, the
	// Logout endpoint skips the upstream end-session redirect and
	// behaves as it did before SLO.
	ssoSessions SSOSessionStore

	// encryptor decrypts the upstream id_token stored alongside an
	// sso_sessions row. Required by the SLO path; without it the
	// stored ciphertext can't be unwrapped and Logout degrades.
	encryptor *auth.Encryptor

	// postLogoutRedirectURL is the post_logout_redirect_uri value sent
	// to the IdP in the end_session redirect. Typically
	// "<server_url>/api/v1/auth/logout-done/". Empty disables the
	// post-logout-redirect parameter — most IdPs accept that and bounce
	// to their default post-logout page.
	postLogoutRedirectURL string
}

// SetQuotaEnforcer wires the per-tenant quota enforcer for the auth
// handler. Optional; without it CreateToken skips the quota check.
func (h *AuthHandler) SetQuotaEnforcer(e *quota.Enforcer) {
	if h == nil {
		return
	}
	h.enforcer = e
}

// EmailNotifier is the surface AuthHandler (and the other hook-site
// handlers) need to fire-and-forget enqueue an email. Wraps the
// concrete *email.Enqueuer so tests can substitute a tiny counter.
type EmailNotifier interface {
	EnqueueAndLog(ctx context.Context, req EmailNotifierRequest)
}

// EmailNotifierRequest is the package-local shape that maps onto
// email.Request. Declared here so the handler package doesn't have to
// re-export email.Request constructors from every call site.
type EmailNotifierRequest struct {
	To       string
	Template string
	Subject  string
	Data     any
	UserID   uuid.UUID
}

// TOTPEnrollmentGate is the surface AuthHandler needs to decide whether
// to short-circuit a successful bcrypt into a TOTP challenge instead
// of a session. Satisfied by TOTPHandler.IsEnrolled (in production)
// and by trivial test fakes.
type TOTPEnrollmentGate interface {
	IsEnrolled(ctx context.Context, userID uuid.UUID) bool
}

// NewAuthHandler creates a new auth handler.
func NewAuthHandler(queries UserQuerier, jwt *auth.JWTManager) *AuthHandler {
	return &AuthHandler{
		queries: queries,
		jwt:     jwt,
		log:     slog.Default(),
	}
}

// NewAuthHandlerWithTokens creates a new auth handler with token support.
func NewAuthHandlerWithTokens(queries UserQuerier, tokens TokenQuerier, jwt *auth.JWTManager) *AuthHandler {
	return &AuthHandler{
		queries: queries,
		tokens:  tokens,
		jwt:     jwt,
		log:     slog.Default(),
	}
}

// SetPasswordRehasher attaches the rehash hook used by Login() to upgrade
// inherited Django PBKDF2/argon2 hashes to bcrypt on first successful match.
func (h *AuthHandler) SetPasswordRehasher(p PasswordRehasher) {
	h.rehasher = p
}

// SetRoleBindings attaches the queries used by /auth/me/ to surface a user's
// aggregated global/cluster/project role bindings.
func (h *AuthHandler) SetRoleBindings(r RoleBindingsQuerier) {
	h.roles = r
}

// SetAuditWriter wires the audit-log writer used by Login / Logout /
// ChangePassword. Optional; when nil, auth events are not persisted (which
// matches the existing behaviour of the test fakes that don't supply it).
func (h *AuthHandler) SetAuditWriter(a AuthAuditWriter) {
	h.audit = a
}

// SetLogger overrides the handler's logger.
func (h *AuthHandler) SetLogger(log *slog.Logger) {
	if log != nil {
		h.log = log
	}
}

// SetLockoutQuerier wires the account-lockout backend. When unset, Login
// behaves as before (no per-account failure counter, no lockout). The
// threshold + duration come from SetLockoutPolicy; defaults are read
// from internal/auth/lockout.go.
func (h *AuthHandler) SetLockoutQuerier(q LockoutQuerier) {
	h.lockout = q
}

// SetRevocationQuerier wires the JWT revocation deny-list + per-user
// invalidation cutoff backend. When unset, Logout is a no-op and
// force-logout cannot be served.
func (h *AuthHandler) SetRevocationQuerier(q RevocationQuerier) {
	h.revocation = q
}

// SetSSOSessionStore wires the sso_sessions reader/deleter used by
// Logout to drive RP-initiated single sign-out (migration 054). When
// nil, Logout falls back to "JWT revoked locally only" — same as
// pre-054 behaviour.
func (h *AuthHandler) SetSSOSessionStore(s SSOSessionStore) {
	if h == nil {
		return
	}
	h.ssoSessions = s
}

// SetEncryptor wires the Fernet encryptor used by Logout to decrypt
// the upstream id_token stored on an sso_sessions row. Required by
// the SLO path. nil keeps the legacy local-only logout shape.
func (h *AuthHandler) SetEncryptor(e *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = e
}

// SetPostLogoutRedirectURL configures the post_logout_redirect_uri
// passed to the IdP's end_session redirect. Typically
// "<server_url>/api/v1/auth/logout-done/". Empty omits the parameter
// — most IdPs accept that and fall back to their default page.
func (h *AuthHandler) SetPostLogoutRedirectURL(u string) {
	if h == nil {
		return
	}
	h.postLogoutRedirectURL = u
}

// SetLockoutPolicy overrides the failure threshold + lockout duration
// from the chart-tuned config. Zero values keep the package defaults.
func (h *AuthHandler) SetLockoutPolicy(threshold int, duration time.Duration) {
	if threshold > 0 {
		h.failThresh = threshold
	}
	if duration > 0 {
		h.lockoutDur = duration
	}
}

// SetTOTPGate wires the 2FA enrollment-check used by Login to gate the
// session-issue path. Passing nil keeps the legacy password-only flow.
func (h *AuthHandler) SetTOTPGate(g TOTPEnrollmentGate) {
	h.totpGate = g
}

// SetEmailNotifier attaches the email-enqueue hook used by the
// lockout + create-token paths. Optional; when nil, the audit row is
// still written but no email is enqueued.
func (h *AuthHandler) SetEmailNotifier(n EmailNotifier) { h.emails = n }

// SetPasswordResetStore attaches the password-reset token store used
// by /auth/password-reset/request|complete. Optional; when nil, those
// endpoints respond 503.
func (h *AuthHandler) SetPasswordResetStore(s PasswordResetStore) { h.passwordResets = s }

// PasswordResetStore is the database surface needed by the password
// reset flow. Implemented by *sqlc.Queries.
type PasswordResetStore interface {
	CreatePasswordResetToken(ctx context.Context, arg sqlc.CreatePasswordResetTokenParams) (sqlc.PasswordResetToken, error)
	GetPasswordResetTokenByHash(ctx context.Context, tokenHash string) (sqlc.PasswordResetToken, error)
	ConsumePasswordResetToken(ctx context.Context, arg sqlc.ConsumePasswordResetTokenParams) (int64, error)
	DeletePasswordResetTokensForUser(ctx context.Context, userID uuid.UUID) error
	UpdateUserPassword(ctx context.Context, arg sqlc.UpdateUserPasswordParams) error
}

// SetTOTPRequireAll flips the chart-tuned auth.totp.require knob. When
// true, every local-password user must be enrolled — the post-bcrypt
// path returns an enrollment-only challenge for unenrolled accounts.
func (h *AuthHandler) SetTOTPRequireAll(require bool) {
	h.totpRequireAll = require
}

// effectiveLockoutPolicy returns the runtime threshold + duration with
// the package-level defaults filled in. Keeps the Login handler's
// branching tidy.
func (h *AuthHandler) effectiveLockoutPolicy() (int, time.Duration) {
	t := h.failThresh
	if t <= 0 {
		t = auth.LoginFailureThreshold
	}
	d := h.lockoutDur
	if d <= 0 {
		d = auth.LockoutDuration
	}
	return t, d
}

// LoginRequest represents the login request body.
type LoginRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// UserResponse represents the user data in login response.
type UserResponse struct {
	ID                 string  `json:"id"`
	Email              string  `json:"email"`
	Username           string  `json:"username"`
	FirstName          string  `json:"first_name"`
	LastName           string  `json:"last_name"`
	IsActive           bool    `json:"is_active"`
	IsStaff            bool    `json:"is_staff"`
	IsSuperuser        bool    `json:"is_superuser"`
	DateJoined         string  `json:"date_joined"`
	LastLogin          *string `json:"last_login"`
	MustChangePassword bool    `json:"must_change_password"`
}

// LoginResponse matches the Python AstronomerTokenObtainPairSerializer.
type LoginResponse struct {
	Token   string       `json:"token"`
	Refresh string       `json:"refresh"`
	User    UserResponse `json:"user"`
}

type refreshRequest struct {
	Refresh string `json:"refresh"`
}

func userToResponse(user sqlc.User) UserResponse {
	var lastLogin *string
	if user.LastLogin.Valid {
		s := user.LastLogin.Time.UTC().Format("2006-01-02T15:04:05Z")
		lastLogin = &s
	}

	return UserResponse{
		ID:                 user.ID.String(),
		Email:              user.Email,
		Username:           user.Username,
		FirstName:          user.FirstName,
		LastName:           user.LastName,
		IsActive:           user.IsActive,
		IsStaff:            user.IsStaff,
		IsSuperuser:        user.IsSuperuser,
		DateJoined:         user.DateJoined.UTC().Format("2006-01-02T15:04:05Z"),
		LastLogin:          lastLogin,
		MustChangePassword: user.MustChangePassword,
	}
}

// Login handles POST /api/v1/auth/login/.
// Accepts {username, password} or {email, password}.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Email == "" && req.Username == "" {
		RespondError(w, http.StatusBadRequest, "missing_credentials", "Email or username is required")
		return
	}

	if req.Password == "" {
		RespondError(w, http.StatusBadRequest, "missing_credentials", "Password is required")
		return
	}

	ctx := r.Context()
	var user sqlc.User
	var err error

	// The frontend form is labelled "Email" but submits its value in the
	// `username` field (legacy axios contract). Accept either: try email lookup
	// when the value contains '@', otherwise try username, then fall back to
	// the other shape so users can log in with either credential.
	identifier := req.Username
	if req.Email != "" {
		identifier = req.Email
	}
	if strings.Contains(identifier, "@") {
		user, err = h.queries.GetUserByEmail(ctx, identifier)
		if err != nil {
			user, err = h.queries.GetUserByUsername(ctx, identifier)
		}
	} else {
		user, err = h.queries.GetUserByUsername(ctx, identifier)
		if err != nil {
			user, err = h.queries.GetUserByEmail(ctx, identifier)
		}
	}

	if err != nil {
		// User-not-found is recorded under the attempted identifier so a
		// brute-force scan for valid usernames is visible in the audit
		// stream. user_id stays NULL because there's no row to attribute.
		recordAuditAs(r, h.audit, pgtype.UUID{}, "auth.login_failed", "user", "", identifier, map[string]any{
			"reason": "user_not_found",
		})
		RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid credentials")
		return
	}

	// Account-lockout gate. Sits BEFORE bcrypt so a locked account
	// can't be probed for password validity (which would also chew
	// CPU). An expired lock falls through naturally because the
	// timestamp comparison returns false. NIST 800-53 AC-7.
	if user.LockedUntil.Valid && user.LockedUntil.Time.After(time.Now()) {
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true},
			"auth.login_locked", "user", user.ID.String(), user.Username, map[string]any{
				"reason":       "account_locked",
				"locked_until": user.LockedUntil.Time.UTC().Format(time.RFC3339),
				"locked_reason": user.LockedReason,
			})
		// 423 Locked is the RFC 4918 status that fits best; we keep
		// the JSON error envelope shape unchanged so the frontend
		// can surface "account_locked" without parsing the status.
		RespondError(w, http.StatusLocked, "account_locked", "Account is temporarily locked. Try again later or contact an administrator.")
		return
	}

	ok, needsRehash, verifyErr := auth.VerifyPassword(user.Password, req.Password)
	if verifyErr != nil {
		// A malformed stored hash is treated as a credential failure to avoid
		// leaking schema details. The error is logged for operators.
		if h.log != nil {
			h.log.Warn("password verification error", "user_id", user.ID.String(), "error", verifyErr)
		}
		h.handleFailedAttempt(ctx, r, user, "verify_error")
		RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid credentials")
		return
	}
	if !ok {
		h.handleFailedAttempt(ctx, r, user, "bad_password")
		RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid credentials")
		return
	}

	if !user.IsActive {
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true}, "auth.login_failed", "user", user.ID.String(), user.Username, map[string]any{
			"reason": "account_disabled",
		})
		RespondError(w, http.StatusForbidden, "account_disabled", "Account is disabled")
		return
	}

	// Successful auth — reset failure counter + clear any expired lock
	// from a prior cycle. Best-effort; failure here doesn't block login.
	if h.lockout != nil {
		if err := h.lockout.ResetFailedLoginCount(ctx, user.ID); err != nil && h.log != nil {
			h.log.Warn("failed to reset login failure counter", "user_id", user.ID.String(), "error", err)
		}
	}

	// Opportunistically upgrade legacy Django PBKDF2/argon2 hashes to bcrypt.
	// Failure here is non-fatal — we still log the user in.
	if needsRehash && h.rehasher != nil {
		if newHash, hashErr := auth.HashPassword(req.Password); hashErr == nil {
			if err := h.rehasher.UpdateUserPasswordHash(ctx, sqlc.UpdateUserPasswordHashParams{
				ID:       user.ID,
				Password: newHash,
			}); err != nil && h.log != nil {
				h.log.Warn("failed to rehash password", "user_id", user.ID.String(), "error", err)
			}
		} else if h.log != nil {
			h.log.Warn("failed to compute bcrypt hash for rehash", "user_id", user.ID.String(), "error", hashErr)
		}
	}

	// 2FA gate. After bcrypt success, check whether this user has a
	// confirmed TOTP enrollment. If so, do NOT issue the session pair
	// — instead return a short-lived challenge token and 423 Locked
	// (RFC 4918 — body carries the next-step machine-readable code).
	// The browser flow swaps the challenge for a real session via
	// POST /auth/totp/verify.
	if h.totpGate != nil && h.totpGate.IsEnrolled(ctx, user.ID) {
		challenge, gerr := h.jwt.GeneratePurposeToken(user.ID, auth.PurposeTOTPChallenge, auth.TOTPChallengeTTL)
		if gerr != nil {
			RespondError(w, http.StatusInternalServerError, "token_error", "Failed to mint TOTP challenge")
			return
		}
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true},
			"auth.login_totp_required", "user", user.ID.String(), user.Username, map[string]any{
				"identifier_type": loginIdentifierType(identifier),
			})
		RespondJSONUnwrapped(w, http.StatusLocked, map[string]any{
			"error":           "totp_required",
			"challenge_token": challenge,
		})
		return
	}

	// require=true enforcement: the account passed password but hasn't
	// enrolled. Hand back an enrollment-only challenge so the SPA can
	// drive the user through the QR flow before letting them in.
	if h.totpRequireAll && h.totpGate != nil && !h.totpGate.IsEnrolled(ctx, user.ID) {
		enrollChallenge, gerr := h.jwt.GeneratePurposeToken(user.ID, auth.PurposeTOTPEnrollOnly, auth.TOTPChallengeTTL)
		if gerr != nil {
			RespondError(w, http.StatusInternalServerError, "token_error", "Failed to mint enrollment challenge")
			return
		}
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true},
			"auth.login_totp_enroll_required", "user", user.ID.String(), user.Username, nil)
		RespondJSONUnwrapped(w, http.StatusLocked, map[string]any{
			"error":           "totp_enrollment_required",
			"challenge_token": enrollChallenge,
		})
		return
	}

	accessToken, refreshToken, err := h.jwt.GenerateTokenPair(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "token_error", "Failed to generate token")
		return
	}

	// Update last_login (best-effort; don't fail the login if this errors)
	_ = h.queries.UpdateUserLastLogin(ctx, user.ID)

	resp := LoginResponse{
		Token:   accessToken,
		Refresh: refreshToken,
		User:    userToResponse(user),
	}

	recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true},
		"auth.login", "user", user.ID.String(), user.Username, map[string]any{
			"identifier_type": loginIdentifierType(identifier),
		},
	)

	RespondJSON(w, http.StatusOK, resp)
}

// loginIdentifierType labels how the user logged in (email vs username) for
// the audit detail. Pure presentation; no security boundary.
func loginIdentifierType(identifier string) string {
	if strings.Contains(identifier, "@") {
		return "email"
	}
	return "username"
}

// handleFailedAttempt is the shared post-bcrypt-miss branch: increment
// the per-user failure counter, lock the account when the threshold is
// reached, and emit the audit row. Best-effort — every DB error is
// logged but never blocks the HTTP response (the caller already returned
// the user-facing 401).
//
// `reason` distinguishes between "bcrypt mismatch" (bad_password) and
// "stored hash unparseable" (verify_error); both count toward the
// threshold because either way the caller didn't prove possession of
// the credential.
func (h *AuthHandler) handleFailedAttempt(ctx context.Context, r *http.Request, user sqlc.User, reason string) {
	threshold, lockDur := h.effectiveLockoutPolicy()

	// Default audit row mirrors the legacy bad-password path so
	// downstream consumers don't have to special-case the new
	// columns.
	auditDetail := map[string]any{
		"reason":               reason,
		"failed_login_count":   int(user.FailedLoginCount) + 1, // optimistic — DB roundtrip below
		"lockout_threshold":    threshold,
	}

	if h.lockout != nil {
		now := time.Now()
		if err := h.lockout.IncrementFailedLoginCount(ctx, sqlc.IncrementFailedLoginCountParams{
			ID:            user.ID,
			FailedLoginAt: pgtype.Timestamptz{Time: now, Valid: true},
		}); err != nil {
			if h.log != nil {
				h.log.Warn("failed to increment failed-login count", "user_id", user.ID.String(), "error", err)
			}
		}

		// Threshold check uses the OLD value + 1 because we just
		// observed the increment. If the row was already at
		// (threshold-1), this attempt is the one that crosses it.
		if int(user.FailedLoginCount)+1 >= threshold {
			lockedUntil := now.Add(lockDur)
			if err := h.lockout.LockUser(ctx, sqlc.LockUserParams{
				ID:           user.ID,
				LockedUntil:  pgtype.Timestamptz{Time: lockedUntil, Valid: true},
				LockedReason: auth.LockoutReasonTooManyFailedAttempts,
			}); err != nil {
				if h.log != nil {
					h.log.Warn("failed to lock user", "user_id", user.ID.String(), "error", err)
				}
			} else {
				auth.AccountLockoutsTotal.WithLabelValues(observability.MetricValues(auth.LockoutReasonTooManyFailedAttempts)...).Inc()
				auditDetail["locked"] = true
				auditDetail["locked_until"] = lockedUntil.UTC().Format(time.RFC3339)
				// Best-effort: fire the account_locked email. The
				// audit row carries the lock either way; this just
				// tells the user their account is locked.
				if h.emails != nil && user.Email != "" {
					h.emails.EnqueueAndLog(ctx, EmailNotifierRequest{
						To:       user.Email,
						Template: "account_locked",
						Data: map[string]any{
							"Username": user.Username,
							"UnlockAt": lockedUntil.UTC().Format(time.RFC3339),
						},
						UserID: user.ID,
					})
				}
				recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true},
					"auth.login_locked", "user", user.ID.String(), user.Username, auditDetail)
				return
			}
		}
	}

	recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true},
		"auth.login_failed", "user", user.ID.String(), user.Username, auditDetail)
}

// Refresh handles POST /api/v1/auth/refresh/.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	claims, err := h.jwt.ValidateToken(req.Refresh)
	if err != nil {
		recordAuditAs(r, h.audit, pgtype.UUID{}, "auth.refresh_failed", "user", "", "", map[string]any{
			"reason": "invalid_token",
		})
		RespondError(w, http.StatusUnauthorized, "invalid_token", "Invalid refresh token")
		return
	}
	if claims.TokenType != auth.RefreshToken {
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: claims.UserID, Valid: true}, "auth.refresh_failed", "user", claims.UserID.String(), "", map[string]any{
			"reason": "wrong_token_type",
		})
		RespondError(w, http.StatusUnauthorized, "invalid_token", "Invalid refresh token")
		return
	}

	user, err := h.queries.GetUserByID(r.Context(), claims.UserID)
	if err != nil || !user.IsActive {
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: claims.UserID, Valid: true}, "auth.refresh_failed", "user", claims.UserID.String(), "", map[string]any{
			"reason": "user_not_active",
		})
		RespondError(w, http.StatusUnauthorized, "invalid_token", "Invalid refresh token")
		return
	}

	accessToken, refreshToken, err := h.jwt.GenerateTokenPair(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "token_error", "Failed to generate token")
		return
	}

	recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true}, "auth.refresh", "user", user.ID.String(), user.Username, nil)

	RespondJSON(w, http.StatusOK, map[string]string{
		"token":   accessToken,
		"refresh": refreshToken,
	})
}

// Logout handles POST /api/v1/auth/logout/.
//
// JWTs are normally stateless on the server, but with the revocation
// layer wired we add the caller's JTI to the deny list so the
// no-longer-valid token can't be replayed before its natural expiry.
// When the revocation backend is unwired (tests / pre-DB bootstrap),
// the endpoint degrades back to the historical no-op shape: emit the
// audit row and return 200.
//
// Single sign-out (migration 054, NIST 800-53 AC-12 / SOC 2 CC6.6):
// when the caller's JTI has an sso_sessions row (i.e. they logged in
// via an upstream OIDC IdP), the response additionally carries a
// `redirect_url` pointing at the IdP's RP-initiated logout endpoint.
// The frontend follows that redirect so the upstream session is
// terminated too — local JWT revocation alone leaves the IdP's
// cookie intact, which a refresh would re-mint.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	authUser, ok := middleware.GetAuthenticatedUser(r.Context())
	auditDetail := map[string]any{}

	// jtiForSLO carries the parsed JTI of the caller's access token
	// out of the revocation block so the SLO branch can look up the
	// matching sso_sessions row. Empty means "no bearer / no valid
	// JTI" → no SLO redirect.
	var jtiForSLO string

	// Extract the JTI from the bearer JWT so we can add THIS token's
	// JTI to the deny list. We don't trust the AuthenticatedUser to
	// carry it (the middleware doesn't propagate it today), so we
	// parse from the Authorization header.
	if h.revocation != nil && h.jwt != nil {
		if token := bearerTokenFromRequest(r); token != "" {
			if claims, err := h.jwt.ValidateTokenContext(r.Context(), token); err == nil {
				expiresAt := time.Time{}
				if claims.ExpiresAt != nil {
					expiresAt = claims.ExpiresAt.Time
				}
				if expiresAt.IsZero() {
					// Belt-and-braces: a token with no exp shouldn't
					// reach here (ValidateToken rejects), but if it
					// did, hold the deny entry for one access lifetime
					// so it can't be replayed forever.
					expiresAt = time.Now().Add(24 * time.Hour)
				}
				if err := h.revocation.RevokeJWT(r.Context(), sqlc.RevokeJWTParams{
					Jti:       claims.ID,
					UserID:    claims.UserID,
					ExpiresAt: expiresAt,
					Reason:    "user_logout",
				}); err != nil {
					if h.log != nil {
						h.log.Warn("failed to revoke JWT", "user_id", claims.UserID.String(), "jti", claims.ID, "error", err)
					}
				} else {
					auth.SessionRevocationsTotal.WithLabelValues(observability.MetricValues("jti", "user_logout")...).Inc()
					auditDetail["jti"] = claims.ID
					auditDetail["revoked"] = true
					jtiForSLO = claims.ID
					// Drop the cached "this JTI is valid" entry so an in-flight
					// validator running in another worker doesn't accept the
					// same token before TTL expiry.
					h.jwt.InvalidateCache()
				}
			}
		}
	}

	// Build the RP-initiated logout redirect when an upstream SSO
	// session is present for this JTI. The frontend follows the URL
	// with a top-level navigation so Dex (and any back-channel-SLO
	// connector behind it — SAML, certain OIDC providers) tears down
	// the user's session everywhere. Best-effort: every failure path
	// degrades to "no redirect_url" → local logout only.
	redirectURL := ""
	if jtiForSLO != "" {
		redirectURL = h.buildSSOLogoutRedirect(r, jtiForSLO, &auditDetail)
	}

	if ok && authUser != nil {
		recordAudit(r, h.audit, "auth.logout", "user", authUser.ID, authUser.Username, auditDetail)
	} else {
		// Anonymous logout (no auth header / expired) — keep the audit
		// trail so brute-force probes of /logout are still visible.
		recordAuditAs(r, h.audit, pgtype.UUID{}, "auth.logout", "user", "", "", auditDetail)
	}

	resp := map[string]any{"detail": "Logged out"}
	if redirectURL != "" {
		resp["redirect_url"] = redirectURL
	}
	RespondJSONUnwrapped(w, http.StatusOK, resp)
}

// buildSSOLogoutRedirect looks up the upstream session for the caller's
// JTI, decrypts the stored id_token, and constructs the RP-initiated
// logout URL. Returns "" on every "skip this" path: no session row
// (local-password login), no end_session_endpoint advertised by the
// IdP, decrypt failure, or storage misconfigured. Each path emits its
// own audit + metric so an operator debugging "logout didn't kick me
// out of the IdP" can find the answer in the audit stream.
//
// Side effects: increments astronomer_auth_sso_logouts_total and
// deletes the sso_sessions row on every outcome (success OR fallback)
// because the JWT is already revoked and the stored id_token can't be
// reused safely.
func (h *AuthHandler) buildSSOLogoutRedirect(r *http.Request, jti string, auditDetail *map[string]any) string {
	if h == nil || h.ssoSessions == nil || h.encryptor == nil {
		return ""
	}
	session, err := h.ssoSessions.GetSSOSession(r.Context(), jti)
	if err != nil {
		// sql.ErrNoRows is the dominant case here — local-password
		// users, or an SSO session row already cleaned up by a
		// concurrent admin force-logout. Either way: no redirect.
		auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues("", "no_session")...).Inc()
		return ""
	}
	if *auditDetail == nil {
		*auditDetail = map[string]any{}
	}
	(*auditDetail)["sso_provider"] = session.ProviderName

	// Drop the row regardless of whether we end up returning a
	// redirect URL: the JWT is already revoked, the row's id_token
	// will be useless once it expires, and leaving it lying around
	// just exposes the encrypted token to a later DB leak for no
	// benefit.
	defer func() {
		if err := h.ssoSessions.DeleteSSOSession(r.Context(), jti); err != nil && h.log != nil {
			h.log.Warn("failed to delete sso_sessions row", "jti", jti, "error", err)
		}
	}()

	if session.EndSessionEndpoint == "" {
		// IdP doesn't advertise RP-initiated logout (or discovery
		// failed at callback time). Local revocation is the best we
		// can do — record the gap so the SOC 2 dashboard sees it.
		auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues(session.ProviderName, "no_endpoint")...).Inc()
		(*auditDetail)["sso_logout"] = "no_endpoint"
		return ""
	}
	idToken, err := h.encryptor.Decrypt(session.UpstreamIdTokenEncrypted)
	if err != nil {
		auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues(session.ProviderName, "encrypt_error")...).Inc()
		(*auditDetail)["sso_logout"] = "decrypt_failed"
		if h.log != nil {
			h.log.Warn("failed to decrypt upstream id_token", "jti", jti, "provider", session.ProviderName, "error", err)
		}
		return ""
	}

	redirectURL, err := buildEndSessionURL(session.EndSessionEndpoint, idToken, h.postLogoutRedirectURL)
	if err != nil {
		auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues(session.ProviderName, "encrypt_error")...).Inc()
		(*auditDetail)["sso_logout"] = "invalid_endpoint"
		return ""
	}
	auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues(session.ProviderName, "redirected")...).Inc()
	(*auditDetail)["sso_logout"] = "redirected"
	return redirectURL
}

// buildEndSessionURL constructs the RP-initiated logout URL for an
// OIDC IdP. Standalone for testability — the parameter encoding
// matters (some IdPs are strict about URL-encoding of the embedded
// id_token's '=' padding) and we want a focused unit test that
// doesn't have to stand up a Logout handler.
//
// Parameters per the OIDC RP-Initiated Logout 1.0 spec:
//
//   - id_token_hint           — the upstream id_token (required)
//   - post_logout_redirect_uri — where the IdP bounces back to after
//                                tearing down its session (optional;
//                                omitted when empty so a strict IdP
//                                that doesn't have it registered
//                                doesn't 400)
//   - state                   — opaque round-tripped value (best-
//                                effort CSRF marker; the landing
//                                handler doesn't validate it because
//                                /logout-done has no privileged
//                                action to gate)
func buildEndSessionURL(endpoint, idToken, postLogoutRedirectURI string) (string, error) {
	if endpoint == "" {
		return "", errEmptyEndSessionEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	q := parsed.Query()
	q.Set("id_token_hint", idToken)
	if postLogoutRedirectURI != "" {
		q.Set("post_logout_redirect_uri", postLogoutRedirectURI)
	}
	if state, gerr := generateLogoutState(); gerr == nil {
		q.Set("state", state)
	}
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

// errEmptyEndSessionEndpoint is returned by buildEndSessionURL when
// the IdP didn't advertise an end_session_endpoint. Carried as a
// package-level sentinel so tests can assert on the exact error
// without string-matching.
var errEmptyEndSessionEndpoint = &endSessionError{"end_session_endpoint is empty"}

type endSessionError struct{ msg string }

func (e *endSessionError) Error() string { return e.msg }

// generateLogoutState returns a 32-byte URL-safe random string for
// the state parameter of the end-session redirect. The same shape
// as the SSO Login state — see internal/auth/oauth.go for the
// rationale (CSRF marker, opaque to the IdP).
func generateLogoutState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// LogoutDone handles GET /api/v1/auth/logout-done/.
//
// This is the post_logout_redirect_uri the SSO Logout flow sends the
// IdP to bounce back to. The endpoint is intentionally minimal: it
// sets a one-shot "logged_out" cookie so the SPA can render a
// confirmation page on the next nav, and redirects to the dashboard's
// login screen. Auth: PUBLIC — by the time the IdP redirects here
// the user has already passed the bearer-revocation step, and the
// frontend is what actually decides what to render.
func (h *AuthHandler) LogoutDone(w http.ResponseWriter, r *http.Request) {
	// Best-effort marker cookie the SPA reads to flash "you've been
	// signed out everywhere" on the login page. Short-lived and not
	// signed — there's no security boundary here, just a UX hint.
	http.SetCookie(w, &http.Cookie{
		Name:     "astro_logged_out",
		Value:    "1",
		Path:     "/",
		HttpOnly: false, // SPA-readable
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60,
	})
	// Audit the landing so the SOC 2 retention bundle includes the
	// full SLO loop: redirect issued → IdP processed → user returned.
	recordAuditAs(r, h.audit, pgtype.UUID{}, "auth.sso_logout_completed", "user", "", "", nil)
	// 303 See Other so the bouncing browser switches to GET regardless
	// of the IdP's choice of redirect method. /dashboard/login is the
	// SPA's marketed entrypoint; if the SPA isn't served by this
	// process the operator can override this later (the path is fixed
	// here only because it has no chart-level knob today).
	http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
}

// bearerTokenFromRequest extracts the JWT from the Authorization header.
// Used by Logout to pull the caller's JTI for the deny list — the auth
// middleware doesn't currently propagate the JTI into the AuthenticatedUser
// struct so we re-parse here. Returns empty when no bearer is present.
func bearerTokenFromRequest(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

// ChangePasswordRequest is the body for POST /api/v1/auth/change-password/.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword handles POST /api/v1/auth/change-password/.
//
// Verifies the caller's current password, hashes the new one with bcrypt, and
// persists it via UpdateUserPasswordHash. Requires the auth middleware to have
// populated the request context with the authenticated user.
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	authUser, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	userID, err := uuid.Parse(authUser.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}

	var req ChangePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "current_password and new_password are required")
		return
	}

	dbUser, err := h.queries.GetUserByID(r.Context(), userID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	verified, _, verifyErr := auth.VerifyPassword(dbUser.Password, req.CurrentPassword)
	if verifyErr != nil || !verified {
		RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Current password is incorrect")
		return
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "hash_error", "Failed to hash new password")
		return
	}

	if h.rehasher == nil {
		RespondError(w, http.StatusServiceUnavailable, "not_configured", "Password updates are not configured")
		return
	}
	if err := h.rehasher.UpdateUserPasswordHash(r.Context(), sqlc.UpdateUserPasswordHashParams{
		ID:       userID,
		Password: newHash,
	}); err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update password")
		return
	}

	// Bootstrap admins are flagged must_change_password=true; clear the flag
	// here so the frontend stops redirecting to the forced-change screen.
	// Non-bootstrap users have the flag false and this is a no-op.
	if dbUser.MustChangePassword {
		if err := h.rehasher.ClearMustChangePassword(r.Context(), userID); err != nil {
			// Log + audit but don't fail the request: the password has
			// already been rotated, the worst case is the user sees the
			// change-password screen once more.
			if h.log != nil {
				h.log.Warn("failed to clear must_change_password flag", "user_id", userID.String(), "error", err)
			}
		}
	}

	recordAudit(r, h.audit, "auth.change_password", "user", dbUser.ID.String(), dbUser.Username, nil)

	RespondJSONUnwrapped(w, http.StatusOK, map[string]string{"detail": "Password updated"})
}

// CurrentUser handles GET /api/v1/auth/me/.
func (h *AuthHandler) CurrentUser(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}

	dbUser, err := h.queries.GetUserByID(r.Context(), userID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	resp := map[string]any{
		"id":                   dbUser.ID.String(),
		"email":                dbUser.Email,
		"username":             dbUser.Username,
		"first_name":           dbUser.FirstName,
		"last_name":            dbUser.LastName,
		"is_active":            dbUser.IsActive,
		"is_staff":             dbUser.IsStaff,
		"is_superuser":         dbUser.IsSuperuser,
		"must_change_password": dbUser.MustChangePassword,
		"date_joined":          dbUser.DateJoined.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if dbUser.LastLogin.Valid {
		resp["last_login"] = dbUser.LastLogin.Time.UTC().Format("2006-01-02T15:04:05Z")
	} else {
		resp["last_login"] = nil
	}
	resp["roles"] = h.collectRoles(r.Context(), userID)

	RespondJSON(w, http.StatusOK, resp)
}

// collectRoles returns the user's aggregated global/cluster/project role
// bindings in the same shape as the Python /auth/me/ response. The map is
// always populated with empty slices when the role querier is unconfigured.
//
// Was a 1-2-3-fan-out (3 ListBindings + N GetRoleByID) which the frontend
// triggered on every page navigation via /auth/me polling. Now a single
// UNION-ALL query that returns scope+role rules per row.
func (h *AuthHandler) collectRoles(ctx context.Context, userID uuid.UUID) map[string]any {
	out := map[string]any{
		"global":  []any{},
		"cluster": []any{},
		"project": []any{},
	}
	if h.roles == nil {
		return out
	}
	pgID := pgtype.UUID{Bytes: userID, Valid: true}

	rows, err := h.roles.ListUserBindingsWithRoles(ctx, pgID)
	if err != nil {
		if h.log != nil {
			h.log.Warn("failed to load user role bindings", "error", err)
		}
		return out
	}

	globals := make([]map[string]any, 0)
	clusters := make([]map[string]any, 0)
	projects := make([]map[string]any, 0)
	for _, row := range rows {
		base := map[string]any{
			"id":         row.BindingID.String(),
			"role_id":    row.RoleID.String(),
			"role_name":  row.RoleName,
			"role_rules": json.RawMessage(row.RoleRules),
			"group":      row.Group,
		}
		switch row.Scope {
		case "global":
			globals = append(globals, base)
		case "cluster":
			if row.ClusterID.Valid {
				base["cluster_id"] = uuid.UUID(row.ClusterID.Bytes).String()
			} else {
				base["cluster_id"] = ""
			}
			clusters = append(clusters, base)
		case "project":
			if row.ProjectID.Valid {
				base["project_id"] = uuid.UUID(row.ProjectID.Bytes).String()
			} else {
				base["project_id"] = ""
			}
			projects = append(projects, base)
		}
	}

	out["global"] = globals
	out["cluster"] = clusters
	out["project"] = projects
	return out
}

// --- API Token CRUD ---

// CreateTokenRequest represents the request body for creating an API token.
type CreateTokenRequest struct {
	Name          string   `json:"name"`
	ExpiresInDays int      `json:"expires_in_days"`
	Scopes        []string `json:"scopes"`
	// AllowedCIDRs is a comma-separated CIDR list (migration 044). Empty
	// preserves the pre-044 behaviour (no IP restriction). Both bare
	// IPv4/IPv6 and CIDR forms are accepted; we re-serialise into the
	// stored canonical form before persisting.
	AllowedCIDRs string `json:"allowed_cidrs"`
}

// CreateTokenResponse is returned once after token creation, including the plaintext.
type CreateTokenResponse struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Token        string   `json:"token"`
	Prefix       string   `json:"prefix"`
	ExpiresAt    *string  `json:"expires_at"`
	CreatedAt    string   `json:"created_at"`
	Scopes       []string `json:"scopes"`
	AllowedCIDRs string   `json:"allowed_cidrs"`
}

// TokenListItem is a single token in the list response (no plaintext).
type TokenListItem struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Prefix           string   `json:"prefix"`
	ExpiresAt        *string  `json:"expires_at"`
	LastUsedAt       *string  `json:"last_used_at"`
	IsRevoked        bool     `json:"is_revoked"`
	CreatedAt        string   `json:"created_at"`
	Scopes           []string `json:"scopes"`
	AllowedCIDRs     string   `json:"allowed_cidrs"`
	LastSeenRemoteIP string   `json:"last_seen_remote_ip"`
}

// generateAPIToken creates a random API token with prefix, hash, and prefix string.
func generateAPIToken() (plaintext, hash, prefix string, err error) {
	b := make([]byte, 48)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	plaintext = "astro_" + base64.URLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(h[:])
	prefix = plaintext[:12]
	return plaintext, hash, prefix, nil
}

// CreateToken handles POST /api/v1/auth/tokens/.
func (h *AuthHandler) CreateToken(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}

	if h.tokens == nil {
		RespondError(w, http.StatusInternalServerError, "not_configured", "Token management is not configured")
		return
	}

	var req CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Token name is required")
		return
	}

	plaintext, tokenHash, prefix, err := generateAPIToken()
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "token_generation_error", "Failed to generate token")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}

	// Per-user token cap (migration 051). Soft enforcement allows the
	// create but emits a metric; hard returns a 429 + structured body.
	if h.enforcer != nil {
		if err := h.enforcer.CheckUserTokenCreate(r.Context(), userID); err != nil {
			if qe, ok := quota.IsQuotaExceeded(err); ok {
				WriteQuotaExceeded(w, qe)
				return
			}
			RespondError(w, http.StatusInternalServerError, "quota_check_error", "Failed to evaluate token quota")
			return
		}
	}

	var expiresAt pgtype.Timestamptz
	if req.ExpiresInDays > 0 {
		expiresAt = pgtype.Timestamptz{
			Time:  time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour),
			Valid: true,
		}
	}

	scopes, _ := json.Marshal(req.Scopes)
	if req.Scopes == nil {
		scopes = json.RawMessage(`[]`)
	}

	// Validate the CIDR list up-front so an operator typo fails the
	// CREATE with a 400 instead of a silent allow-everything row.
	// Empty string is the legacy "no IP restriction" mode and skips
	// the check entirely. We persist the user's raw string so the
	// CRUD UI round-trips byte-identically; parsing happens at auth
	// time.
	allowedCIDRs := strings.TrimSpace(req.AllowedCIDRs)
	if allowedCIDRs != "" {
		if _, perr := auth.ParseAllowedCIDRs(allowedCIDRs); perr != nil {
			RespondError(w, http.StatusBadRequest, "validation_error",
				"allowed_cidrs must be a comma-separated list of valid CIDR ranges or IP addresses")
			return
		}
	}

	token, err := h.tokens.CreateAPIToken(r.Context(), sqlc.CreateAPITokenParams{
		UserID:       userID,
		Name:         req.Name,
		TokenHash:    tokenHash,
		Prefix:       prefix,
		ExpiresAt:    expiresAt,
		Scopes:       scopes,
		AllowedCidrs: allowedCIDRs,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create token")
		return
	}

	// The audit row captures the scope set + whether an IP allowlist
	// was attached (count, not the literal CIDRs — those are operator-
	// supplied operational data, not credentials, but the count is what
	// reviewers actually need to flag "this token is unrestricted").
	cidrCount := 0
	if allowedCIDRs != "" {
		cidrCount = strings.Count(allowedCIDRs, ",") + 1
	}
	recordAudit(r, h.audit, "auth.token.create", "api_token", token.ID.String(), token.Name, map[string]any{
		"prefix":             token.Prefix,
		"expires_in_days":    req.ExpiresInDays,
		"scopes":             req.Scopes,
		"allowed_cidr_count": cidrCount,
	})

	// Security-FYI email — "a new token was issued; if this wasn't
	// you...". Best-effort, never blocks the response.
	if h.emails != nil {
		// Look up the user row for the email; the middleware-supplied
		// `user` shape only carries ID/username/email summaries.
		if u, err := h.queries.GetUserByID(r.Context(), userID); err == nil && u.Email != "" {
			h.emails.EnqueueAndLog(r.Context(), EmailNotifierRequest{
				To:       u.Email,
				Template: "api_token_created",
				Data: map[string]any{
					"Username":    u.Username,
					"TokenName":   token.Name,
					"TokenPrefix": token.Prefix,
					"CreatedAt":   token.CreatedAt.UTC().Format(time.RFC3339),
				},
				UserID: userID,
			})
		}
	}

	var expiresAtStr *string
	if token.ExpiresAt.Valid {
		s := token.ExpiresAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		expiresAtStr = &s
	}

	respScopes := req.Scopes
	if respScopes == nil {
		respScopes = []string{}
	}
	RespondJSON(w, http.StatusCreated, CreateTokenResponse{
		ID:           token.ID.String(),
		Name:         token.Name,
		Token:        plaintext,
		Prefix:       token.Prefix,
		ExpiresAt:    expiresAtStr,
		CreatedAt:    token.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Scopes:       respScopes,
		AllowedCIDRs: token.AllowedCidrs,
	})
}

// ListTokens handles GET /api/v1/auth/tokens/.
func (h *AuthHandler) ListTokens(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}

	if h.tokens == nil {
		RespondError(w, http.StatusInternalServerError, "not_configured", "Token management is not configured")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}

	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	tokens, err := h.tokens.ListTokensByUser(r.Context(), sqlc.ListTokensByUserParams{
		UserID: userID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list tokens")
		return
	}

	total, err := h.tokens.CountTokensByUser(r.Context(), userID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count tokens")
		return
	}

	items := make([]TokenListItem, 0, len(tokens))
	for _, t := range tokens {
		scopes, _ := auth.ParseTokenScopes(t.Scopes)
		if scopes == nil {
			scopes = []string{}
		}
		item := TokenListItem{
			ID:               t.ID.String(),
			Name:             t.Name,
			Prefix:           t.Prefix,
			IsRevoked:        t.IsRevoked,
			CreatedAt:        t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Scopes:           scopes,
			AllowedCIDRs:     t.AllowedCidrs,
			LastSeenRemoteIP: t.LastSeenRemoteIp,
		}
		if t.ExpiresAt.Valid {
			s := t.ExpiresAt.Time.UTC().Format("2006-01-02T15:04:05Z")
			item.ExpiresAt = &s
		}
		if t.LastUsedAt.Valid {
			s := t.LastUsedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
			item.LastUsedAt = &s
		}
		items = append(items, item)
	}

	RespondPaginated(w, r, items, total)
}

// RevokeToken handles DELETE /api/v1/auth/tokens/{id}/.
func (h *AuthHandler) RevokeToken(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}

	if h.tokens == nil {
		RespondError(w, http.StatusInternalServerError, "not_configured", "Token management is not configured")
		return
	}

	tokenIDStr := chi.URLParam(r, "id")
	tokenID, err := uuid.Parse(tokenIDStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid token ID")
		return
	}

	// Verify the token belongs to the authenticated user.
	token, err := h.tokens.GetAPITokenByID(r.Context(), tokenID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Token not found")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}

	if token.UserID != userID {
		RespondError(w, http.StatusNotFound, "not_found", "Token not found")
		return
	}

	if err := h.tokens.RevokeAPIToken(r.Context(), tokenID); err != nil {
		RespondError(w, http.StatusInternalServerError, "revoke_error", "Failed to revoke token")
		return
	}

	recordAudit(r, h.audit, "auth.token.revoke", "api_token", token.ID.String(), token.Name, map[string]any{
		"prefix": token.Prefix,
	})

	w.WriteHeader(http.StatusNoContent)
}
