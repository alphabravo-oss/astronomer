package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/sessionpolicy"
)

// UserQuerier abstracts the user-related database queries needed by AuthHandler.
// This allows for easy testing with mock implementations.
type UserQuerier interface {
	GetUserByEmail(ctx context.Context, email string) (sqlc.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
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
	GetLatestSSOSessionByUser(ctx context.Context, userID uuid.UUID) (sqlc.SsoSession, error)
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
// It also clears the must_change_password flag after a successful password
// change via the dashboard.
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

// AuthMutationTx is the transaction-bound surface for self-service credential
// mutations. It prevents password/token state from committing when mandatory
// audit evidence or session invalidation cannot be persisted.
type AuthMutationTx interface {
	audit.OutboxQuerier
	GetUserByIDForUpdate(context.Context, uuid.UUID) (sqlc.User, error)
	RecordFailedLoginAttempt(context.Context, sqlc.RecordFailedLoginAttemptParams) (sqlc.User, error)
	UpdateUserPasswordHash(context.Context, sqlc.UpdateUserPasswordHashParams) error
	ClearMustChangePassword(context.Context, uuid.UUID) error
	RevokeJWT(context.Context, sqlc.RevokeJWTParams) error
	InvalidateAllTokens(context.Context, sqlc.InvalidateAllTokensParams) error
	CreateAPIToken(context.Context, sqlc.CreateAPITokenParams) (sqlc.ApiToken, error)
	RevokeAPIToken(context.Context, uuid.UUID) error
}

type authRunTxFunc func(context.Context, func(AuthMutationTx) error) error

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	queries    UserQuerier
	tokens     TokenQuerier
	rehasher   PasswordRehasher
	roles      RoleBindingsQuerier
	audit      AuthAuditWriter
	lockout    LockoutQuerier
	revocation RevocationQuerier
	jwt        *auth.JWTManager
	log        *slog.Logger
	lockoutDur time.Duration
	failThresh int

	// totpGate is the optional 2FA enrollment lookup. When wired,
	// Login switches to the challenge-token flow whenever the user
	// has a row in user_totp_enrollments; without it, the legacy
	// password-only path stands. Separate from totpRequireAll so
	// the test fakes can attach the gate without flipping the
	// global enforcement bit.
	totpGate       TOTPEnrollmentGate
	totpRequireAll bool
	// totpPolicy, when set, is consulted at login to read the runtime
	// admin-toggleable `totp.required` platform setting. It is OR'd with
	// the static chart knob (totpRequireAll): either source flipping
	// enforcement on forces unenrolled local-password users into the
	// enroll-only challenge. nil disables the runtime read (test fakes
	// that only want the static knob leave it unset).
	totpPolicy func(ctx context.Context) bool
	// sessionTimeoutMinutes, when set, returns the platform setting
	// session.timeout_minutes so mint/refresh honor compliance-applied
	// session lifetime (DIR-05). nil keeps the JWT manager boot TTL.
	sessionTimeoutMinutes func(ctx context.Context) int
	// settingsCache resolves password.* platform settings for change/
	// reset password (AUTH-R01). Nil falls back to defaults.
	settingsCache *SettingsCache

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
	runTx                 authRunTxFunc
}

var errCurrentPasswordIncorrect = errors.New("current password is incorrect")

type logoutMutationResult struct {
	jti       string
	userID    uuid.UUID
	expiresAt time.Time
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

func (h *AuthHandler) SetRunTx(runTx authRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *AuthHandler) TransactionalAuditWired() bool {
	return h != nil && h.runTx != nil
}

func executeAuthMutation[T any](
	r *http.Request,
	h *AuthHandler,
	mutate func(AuthMutationTx) (T, error),
	fallback func() (T, error),
	describe func(T) clusterAuditEvent,
) (T, error) {
	var zero T
	if h == nil {
		return zero, fmt.Errorf("auth handler is nil")
	}
	if h.runTx != nil {
		var result T
		err := h.runTx(r.Context(), func(q AuthMutationTx) error {
			var mutationErr error
			result, mutationErr = mutate(q)
			if mutationErr != nil {
				return mutationErr
			}
			event := describe(result)
			return recordAuditOutbox(r, q, event.action, event.resourceType, event.resourceID, event.resourceName, event.status, event.detail)
		})
		return result, err
	}
	result, err := fallback()
	if err != nil {
		return zero, err
	}
	event := describe(result)
	recordAudit(r, h.audit, event.action, event.resourceType, event.resourceID, event.resourceName, event.detail)
	return result, nil
}

func (h *AuthHandler) recordCredentialAuditAs(r *http.Request, userID pgtype.UUID, action, resourceType, resourceID, resourceName string, detail map[string]any) error {
	// Preserve narrow unit-fake compatibility. Production always wires both
	// runTx and the audit writer; a missing writer there is a fail-closed
	// composition error.
	if h != nil && h.audit == nil && h.runTx == nil {
		return nil
	}
	return recordMandatoryAuditAs(r, h.audit, userID, action, resourceType, resourceID, resourceName, detail)
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

// SetTOTPPolicy wires the runtime resolver for the admin-toggleable
// `totp.required` platform setting. It is consulted (and OR'd with the
// static SetTOTPRequireAll knob) on every Login, so an operator flipping
// the setting via PUT /admin/settings/totp.required/ enforces MFA without
// a redeploy. nil leaves only the static knob in force.
func (h *AuthHandler) SetTOTPPolicy(fn func(ctx context.Context) bool) {
	h.totpPolicy = fn
}

// SetSessionTimeoutPolicy wires session.timeout_minutes from platform
// settings (DIR-05). Applied immediately before each token mint/refresh.
func (h *AuthHandler) SetSessionTimeoutPolicy(fn func(ctx context.Context) int) {
	h.sessionTimeoutMinutes = fn
}

// SetSettingsCache wires platform settings for password policy (AUTH-R01).
func (h *AuthHandler) SetSettingsCache(c *SettingsCache) {
	if h != nil {
		h.settingsCache = c
	}
}

// passwordPolicy returns the live password policy from platform settings
// when wired, else DefaultPasswordPolicy.
func (h *AuthHandler) passwordPolicy(ctx context.Context) auth.PasswordPolicy {
	if h == nil {
		return auth.DefaultPasswordPolicy()
	}
	return auth.LoadPasswordPolicy(ctx, h.settingsCache)
}

// applySessionTimeoutFromSettings resolves the runtime policy once and carries
// it on the mint context. The JWT provider consumes this value, avoiding a
// second DB read and preventing two reads from observing different settings.
func (h *AuthHandler) applySessionTimeoutFromSettings(ctx context.Context) context.Context {
	if h == nil || h.jwt == nil || h.sessionTimeoutMinutes == nil {
		return ctx
	}
	return sessionpolicy.WithMinutes(ctx, h.sessionTimeoutMinutes(ctx))
}

// totpEnforced reports whether MFA enrollment is mandatory for this
// login, combining the static chart knob with the runtime admin setting.
func (h *AuthHandler) totpEnforced(ctx context.Context) bool {
	if h.totpRequireAll {
		return true
	}
	if h.totpPolicy != nil {
		return h.totpPolicy(ctx)
	}
	return false
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
// openapi:request LoginRequest
type LoginRequest struct {
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

const browserRefreshCookieMaxAge = int((7 * 24 * time.Hour) / time.Second)

func setBrowserSessionCookies(w http.ResponseWriter, r *http.Request, accessToken, refreshToken string) {
	secure := middleware.RequestIsHTTPS(r)
	if csrfToken, err := newBrowserCSRFToken(); err == nil {
		http.SetCookie(w, &http.Cookie{
			Name:     middleware.CSRFCookieName,
			Value:    csrfToken,
			Path:     "/",
			HttpOnly: false,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})
	}
	if accessToken != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     middleware.SessionCookieName,
			Value:    accessToken,
			Path:     "/",
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})
	}
	if refreshToken != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     middleware.RefreshCookieName,
			Value:    refreshToken,
			Path:     "/",
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   browserRefreshCookieMaxAge,
		})
	}
}

func clearBrowserSessionCookies(w http.ResponseWriter, r *http.Request) {
	secure := middleware.RequestIsHTTPS(r)
	for _, name := range []string{middleware.SessionCookieName, middleware.RefreshCookieName, middleware.CSRFCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
	}
}

func newBrowserCSRFToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// openapi:request-operation postAuthRefresh
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
// Accepts {email, password}. Usernames are display/identity metadata only and
// are intentionally not accepted as login identifiers.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}

	email, emailErr := normalizeLoginEmail(req.Email)
	if req.Email == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Email is required")
		return
	}
	if emailErr != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidEmail, "Enter a valid email address")
		return
	}

	if req.Password == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.AuthenticationRequired, "Password is required")
		return
	}

	ctx := r.Context()
	var user sqlc.User
	var err error

	user, err = h.queries.GetUserByEmail(ctx, email)
	if err != nil {
		// User-not-found is recorded under the attempted identifier so a
		// brute-force scan for valid accounts is visible in the audit
		// stream. user_id stays NULL because there's no row to attribute.
		if auditErr := h.recordCredentialAuditAs(r, pgtype.UUID{}, "auth.login_failed", "user", "", email, map[string]any{"reason": "user_not_found"}); auditErr != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
				"Mandatory audit storage is unavailable; authentication is temporarily unavailable")
			return
		}
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Invalid credentials")
		return
	}

	// Account-lockout gate. Sits BEFORE bcrypt so a locked account
	// can't be probed for password validity (which would also chew
	// CPU). An expired lock falls through naturally because the
	// timestamp comparison returns false. NIST 800-53 AC-7.
	if user.LockedUntil.Valid && user.LockedUntil.Time.After(time.Now()) {
		if auditErr := h.recordCredentialAuditAs(r, pgtype.UUID{Bytes: user.ID, Valid: true},
			"auth.login_locked", "user", user.ID.String(), user.Username, map[string]any{
				"reason":        "account_locked",
				"locked_until":  user.LockedUntil.Time.UTC().Format(time.RFC3339),
				"locked_reason": user.LockedReason,
			}); auditErr != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
				"Mandatory audit storage is unavailable; authentication is temporarily unavailable")
			return
		}
		// 423 Locked is the RFC 4918 status that fits best; we keep
		// the JSON error envelope shape unchanged so the frontend
		// can surface "account_locked" without parsing the status.
		RespondRequestError(w, r, http.StatusLocked, apierror.AccountLocked, "Account is temporarily locked. Try again later or contact an administrator.")
		return
	}

	ok, needsRehash, verifyErr := auth.VerifyPassword(user.Password, req.Password)
	if verifyErr != nil {
		// A malformed stored hash is treated as a credential failure to avoid
		// leaking schema details. The error is logged for operators.
		if h.log != nil {
			h.log.Warn("password verification error", "user_id", user.ID.String(), "error", verifyErr)
		}
		if attemptErr := h.handleFailedAttempt(ctx, r, user, "verify_error"); attemptErr != nil {
			respondTransactionalMutationError(w, r, attemptErr, http.StatusServiceUnavailable, apierror.StatusError, "Authentication safeguards are temporarily unavailable")
			return
		}
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Invalid credentials")
		return
	}
	if !ok {
		if attemptErr := h.handleFailedAttempt(ctx, r, user, "bad_password"); attemptErr != nil {
			respondTransactionalMutationError(w, r, attemptErr, http.StatusServiceUnavailable, apierror.StatusError, "Authentication safeguards are temporarily unavailable")
			return
		}
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Invalid credentials")
		return
	}

	if !user.IsActive {
		if auditErr := h.recordCredentialAuditAs(r, pgtype.UUID{Bytes: user.ID, Valid: true}, "auth.login_failed", "user", user.ID.String(), user.Username, map[string]any{"reason": "account_disabled"}); auditErr != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
				"Mandatory audit storage is unavailable; authentication is temporarily unavailable")
			return
		}
		RespondRequestError(w, r, http.StatusForbidden, apierror.AccountDisabled, "Account is disabled")
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
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenError, "Failed to mint TOTP challenge")
			return
		}
		if auditErr := h.recordCredentialAuditAs(r, pgtype.UUID{Bytes: user.ID, Valid: true},
			"auth.login_totp_required", "user", user.ID.String(), user.Username, map[string]any{"identifier_type": "email"}); auditErr != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
				"Mandatory audit storage is unavailable; the authentication challenge was not issued")
			return
		}
		RespondJSONUnwrapped(w, http.StatusLocked, map[string]any{
			"error":           "totp_required",
			"challenge_token": challenge,
		})
		return
	}

	// require=true enforcement: the account passed password but hasn't
	// enrolled. Hand back an enrollment-only challenge so the SPA can
	// drive the user through the QR flow before letting them in.
	if h.totpEnforced(ctx) && h.totpGate != nil && !h.totpGate.IsEnrolled(ctx, user.ID) {
		enrollChallenge, gerr := h.jwt.GeneratePurposeToken(user.ID, auth.PurposeTOTPEnrollOnly, auth.TOTPChallengeTTL)
		if gerr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenError, "Failed to mint enrollment challenge")
			return
		}
		if auditErr := h.recordCredentialAuditAs(r, pgtype.UUID{Bytes: user.ID, Valid: true},
			"auth.login_totp_enroll_required", "user", user.ID.String(), user.Username, nil); auditErr != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
				"Mandatory audit storage is unavailable; the enrollment challenge was not issued")
			return
		}
		RespondJSONUnwrapped(w, http.StatusLocked, map[string]any{
			"error":           "totp_enrollment_required",
			"challenge_token": enrollChallenge,
		})
		return
	}

	mintCtx := h.applySessionTimeoutFromSettings(r.Context())
	accessToken, refreshToken, err := h.jwt.GenerateTokenPairContext(mintCtx, user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenError, "Failed to generate token")
		return
	}

	// Update last_login (best-effort; don't fail the login if this errors)
	_ = h.queries.UpdateUserLastLogin(ctx, user.ID)

	resp := LoginResponse{
		Token:   accessToken,
		Refresh: refreshToken,
		User:    userToResponse(user),
	}

	if auditErr := h.recordCredentialAuditAs(r, pgtype.UUID{Bytes: user.ID, Valid: true},
		"auth.login", "user", user.ID.String(), user.Username, map[string]any{"identifier_type": "email"}); auditErr != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
			"Mandatory audit storage is unavailable; the session was not issued")
		return
	}

	setBrowserSessionCookies(w, r, accessToken, refreshToken)
	RespondJSON(w, http.StatusOK, resp)
}

func normalizeLoginEmail(value string) (string, error) {
	email := strings.TrimSpace(value)
	parsed, err := mail.ParseAddress(email)
	if err != nil {
		return "", err
	}
	if parsed.Address != email {
		return "", fmt.Errorf("display-name email format is not accepted")
	}
	return strings.ToLower(email), nil
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
func (h *AuthHandler) handleFailedAttempt(ctx context.Context, r *http.Request, user sqlc.User, reason string) error {
	threshold, lockDur := h.effectiveLockoutPolicy()
	now := time.Now().UTC()
	lockedUntil := now.Add(lockDur)

	if h.runTx != nil {
		var updated sqlc.User
		err := h.runTx(ctx, func(q AuthMutationTx) error {
			var mutationErr error
			updated, mutationErr = q.RecordFailedLoginAttempt(ctx, sqlc.RecordFailedLoginAttemptParams{
				ID: user.ID, FailedLoginAt: pgtype.Timestamptz{Time: now, Valid: true},
				LockoutThreshold: int32(threshold), LockedUntil: pgtype.Timestamptz{Time: lockedUntil, Valid: true},
				LockedReason: auth.LockoutReasonTooManyFailedAttempts,
			})
			if mutationErr != nil {
				return mutationErr
			}
			locked := updated.LockedUntil.Valid && !updated.LockedUntil.Time.Before(now)
			action := "auth.login_failed"
			if locked {
				action = "auth.login_locked"
			}
			detail := map[string]any{
				"reason": reason, "failed_login_count": updated.FailedLoginCount,
				"lockout_threshold": threshold, "locked": locked,
			}
			if locked {
				detail["locked_until"] = updated.LockedUntil.Time.UTC().Format(time.RFC3339)
			}
			return recordAuditOutboxAs(r, q, pgtype.UUID{Bytes: user.ID, Valid: true},
				action, "user", user.ID.String(), user.Username, http.StatusUnauthorized, detail)
		})
		if err != nil {
			return err
		}
		if updated.LockedUntil.Valid && !updated.LockedUntil.Time.Before(now) {
			auth.AccountLockoutsTotal.WithLabelValues(observability.MetricValues(auth.LockoutReasonTooManyFailedAttempts)...).Inc()
			if h.emails != nil && user.Email != "" {
				h.emails.EnqueueAndLog(ctx, EmailNotifierRequest{
					To: user.Email, Template: "account_locked", UserID: user.ID,
					Data: map[string]any{"Username": user.Username, "UnlockAt": updated.LockedUntil.Time.UTC().Format(time.RFC3339)},
				})
			}
		}
		return nil
	}

	// Default audit row mirrors the legacy bad-password path so
	// downstream consumers don't have to special-case the new
	// columns.
	auditDetail := map[string]any{
		"reason":             reason,
		"failed_login_count": int(user.FailedLoginCount) + 1, // optimistic — DB roundtrip below
		"lockout_threshold":  threshold,
	}

	if h.lockout != nil {
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
				return nil
			}
		}
	}

	recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true},
		"auth.login_failed", "user", user.ID.String(), user.Username, auditDetail)
	return nil
}

// Refresh handles POST /api/v1/auth/refresh/.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
			return
		}
	}
	if strings.TrimSpace(req.Refresh) == "" {
		if c, err := r.Cookie(middleware.RefreshCookieName); err == nil {
			if !middleware.ValidateCSRF(r) {
				RespondRequestError(w, r, http.StatusUnauthorized, apierror.CSRFRequired, "CSRF token is required")
				return
			}
			req.Refresh = c.Value
		}
	}
	if strings.TrimSpace(req.Refresh) == "" {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidToken, "Invalid refresh token")
		return
	}

	claims, err := h.jwt.ValidateToken(req.Refresh)
	if err != nil {
		recordAuditAs(r, h.audit, pgtype.UUID{}, "auth.refresh_failed", "user", "", "", map[string]any{
			"reason": "invalid_token",
		})
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidToken, "Invalid refresh token")
		return
	}
	if claims.TokenType != auth.RefreshToken {
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: claims.UserID, Valid: true}, "auth.refresh_failed", "user", claims.UserID.String(), "", map[string]any{
			"reason": "wrong_token_type",
		})
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidToken, "Invalid refresh token")
		return
	}

	user, err := h.queries.GetUserByID(r.Context(), claims.UserID)
	if err != nil || !user.IsActive {
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: claims.UserID, Valid: true}, "auth.refresh_failed", "user", claims.UserID.String(), "", map[string]any{
			"reason": "user_not_active",
		})
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidToken, "Invalid refresh token")
		return
	}

	// MFA-enforcement gate, mirroring Login. Without this a user who held a live
	// refresh token when enrollment was turned on could roll it forward forever
	// without ever enrolling — the inverse of the enroll-only lockout. Hand back
	// the same enrollment-only challenge so the SPA drives them through the QR
	// flow before a fresh session is issued.
	if h.totpEnforced(r.Context()) && h.totpGate != nil && !h.totpGate.IsEnrolled(r.Context(), user.ID) {
		enrollChallenge, gerr := h.jwt.GeneratePurposeToken(user.ID, auth.PurposeTOTPEnrollOnly, auth.TOTPChallengeTTL)
		if gerr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenError, "Failed to mint enrollment challenge")
			return
		}
		if auditErr := h.recordCredentialAuditAs(r, pgtype.UUID{Bytes: user.ID, Valid: true},
			"auth.refresh_totp_enroll_required", "user", user.ID.String(), user.Username, nil); auditErr != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
				"Mandatory audit storage is unavailable; the enrollment challenge was not issued")
			return
		}
		RespondJSONUnwrapped(w, http.StatusLocked, map[string]any{
			"error":           "totp_enrollment_required",
			"challenge_token": enrollChallenge,
		})
		return
	}

	mintCtx := h.applySessionTimeoutFromSettings(r.Context())
	accessToken, refreshToken, err := h.jwt.GenerateTokenPairContext(mintCtx, user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenError, "Failed to generate token")
		return
	}

	if auditErr := h.recordCredentialAuditAs(r, pgtype.UUID{Bytes: user.ID, Valid: true}, "auth.refresh", "user", user.ID.String(), user.Username, nil); auditErr != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
			"Mandatory audit storage is unavailable; the refreshed session was not issued")
		return
	}

	setBrowserSessionCookies(w, r, accessToken, refreshToken)
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
	// userIDForSLO carries the caller's user id out of the revocation block so
	// the SLO lookup can fall back to the newest sso_sessions row for the user
	// when the access JTI has rotated past a silent refresh (the row is keyed
	// on the login-time access JTI, which no longer matches).
	var userIDForSLO uuid.UUID
	auditRecorded := false

	// Extract the JTI from the bearer JWT so we can add THIS token's
	// JTI to the deny list. We don't trust the AuthenticatedUser to
	// carry it (the middleware doesn't propagate it today), so we
	// parse from the Authorization header.
	if h.revocation != nil && h.jwt != nil {
		if token := bearerTokenFromRequest(r); token != "" {
			if claims, validateErr := h.jwt.ValidateTokenContext(r.Context(), token); validateErr == nil {
				expiresAt := time.Time{}
				if claims.ExpiresAt != nil {
					expiresAt = claims.ExpiresAt.Time
				}
				if expiresAt.IsZero() {
					expiresAt = time.Now().Add(24 * time.Hour)
				}
				revokeParams := sqlc.RevokeJWTParams{Jti: claims.ID, UserID: claims.UserID, ExpiresAt: expiresAt, Reason: "user_logout"}
				invalidateParams := sqlc.InvalidateAllTokensParams{
					ID: claims.UserID, TokensInvalidatedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
				}
				result, mutationErr := executeAuthMutation(r, h,
					func(q AuthMutationTx) (logoutMutationResult, error) {
						if err := q.RevokeJWT(r.Context(), revokeParams); err != nil {
							return logoutMutationResult{}, err
						}
						if err := q.InvalidateAllTokens(r.Context(), invalidateParams); err != nil {
							return logoutMutationResult{}, err
						}
						return logoutMutationResult{jti: claims.ID, userID: claims.UserID, expiresAt: expiresAt}, nil
					},
					func() (logoutMutationResult, error) {
						if err := h.revocation.RevokeJWT(r.Context(), revokeParams); err != nil {
							return logoutMutationResult{}, err
						}
						if err := h.revocation.InvalidateAllTokens(r.Context(), invalidateParams); err != nil {
							return logoutMutationResult{}, err
						}
						return logoutMutationResult{jti: claims.ID, userID: claims.UserID, expiresAt: expiresAt}, nil
					},
					func(result logoutMutationResult) clusterAuditEvent {
						resourceName := ""
						if authUser != nil {
							resourceName = authUser.Username
						}
						return clusterAuditEvent{
							action: "auth.logout", resourceType: "user", resourceID: result.userID.String(), resourceName: resourceName,
							status: http.StatusOK, detail: map[string]any{"jti": result.jti, "revoked": true, "all_tokens_invalidated": true},
						}
					})
				if mutationErr != nil {
					respondTransactionalMutationError(w, r, mutationErr, http.StatusServiceUnavailable, apierror.RevokeError,
						"Logout could not revoke the active session; retry")
					return
				}
				auth.SessionRevocationsTotal.WithLabelValues(observability.MetricValues("jti", "user_logout")...).Inc()
				auditDetail["jti"], auditDetail["revoked"] = result.jti, true
				jtiForSLO, userIDForSLO = result.jti, result.userID
				h.jwt.InvalidateJTI(r.Context(), result.jti)
				h.jwt.InvalidateUser(r.Context(), result.userID)
				auditRecorded = true
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
		redirectURL = h.buildSSOLogoutRedirect(r, jtiForSLO, userIDForSLO, &auditDetail)
	}

	if !auditRecorded {
		actorID := pgtype.UUID{}
		resourceID, resourceName := "", ""
		if ok && authUser != nil {
			resourceID, resourceName = authUser.ID, authUser.Username
			if parsed, parseErr := uuid.Parse(authUser.ID); parseErr == nil {
				actorID = pgtype.UUID{Bytes: parsed, Valid: true}
			}
		}
		if auditErr := h.recordCredentialAuditAs(r, actorID, "auth.logout", "user", resourceID, resourceName, auditDetail); auditErr != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
				"Mandatory audit storage is unavailable; logout was not completed")
			return
		}
	}

	resp := map[string]any{"detail": "Logged out"}
	if redirectURL != "" {
		resp["redirect_url"] = redirectURL
	}
	clearBrowserSessionCookies(w, r)
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
func (h *AuthHandler) buildSSOLogoutRedirect(r *http.Request, jti string, userID uuid.UUID, auditDetail *map[string]any) string {
	if h == nil || h.ssoSessions == nil || h.encryptor == nil {
		return ""
	}
	session, err := h.ssoSessions.GetSSOSession(r.Context(), jti)
	if err != nil {
		// The row is keyed on the LOGIN-time access JTI; after a silent SPA
		// refresh the caller's current JTI no longer matches. Fall back to the
		// newest sso_sessions row for the user so single-sign-out still fires.
		// (id_token can't be re-minted on refresh, so we can't re-key the row.)
		if userID != (uuid.UUID{}) {
			if latest, lerr := h.ssoSessions.GetLatestSSOSessionByUser(r.Context(), userID); lerr == nil {
				session = latest
				jti = latest.Jti // delete the row we actually resolved
				err = nil
			}
		}
	}
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
	idToken, err := h.encryptor.Decrypt(session.UpstreamIDTokenEncrypted)
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
//     tearing down its session (optional;
//     omitted when empty so a strict IdP
//     that doesn't have it registered
//     doesn't 400)
//   - state                   — opaque round-tripped value (best-
//     effort CSRF marker; the landing
//     handler doesn't validate it because
//     /logout-done has no privileged
//     action to gate)
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
		if c, err := r.Cookie(middleware.SessionCookieName); err == nil && strings.TrimSpace(c.Value) != "" {
			return c.Value
		}
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

// ChangePasswordRequest is the body for POST /api/v1/auth/change-password/.
// openapi:request-operation postAuthChangePassword
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
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	userID, err := uuid.Parse(authUser.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}

	var req ChangePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "current_password and new_password are required")
		return
	}
	if err := auth.ValidatePassword(req.NewPassword, h.passwordPolicy(r.Context())); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}

	dbUser, err := h.queries.GetUserByID(r.Context(), userID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "User not found")
		return
	}

	verified, _, verifyErr := auth.VerifyPassword(dbUser.Password, req.CurrentPassword)
	if verifyErr != nil || !verified {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Current password is incorrect")
		return
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.HashError, "Failed to hash new password")
		return
	}

	if h.rehasher == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Password updates are not configured")
		return
	}
	passwordParams := sqlc.UpdateUserPasswordHashParams{ID: userID, Password: newHash}
	invalidateParams := sqlc.InvalidateAllTokensParams{
		ID: userID, TokensInvalidatedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
	dbUser, err = executeAuthMutation(r, h,
		func(q AuthMutationTx) (sqlc.User, error) {
			locked, mutationErr := q.GetUserByIDForUpdate(r.Context(), userID)
			if mutationErr != nil {
				return sqlc.User{}, mutationErr
			}
			verified, _, verifyErr := auth.VerifyPassword(locked.Password, req.CurrentPassword)
			if verifyErr != nil || !verified {
				return sqlc.User{}, errCurrentPasswordIncorrect
			}
			if mutationErr = q.UpdateUserPasswordHash(r.Context(), passwordParams); mutationErr != nil {
				return sqlc.User{}, mutationErr
			}
			if h.revocation != nil {
				if mutationErr = q.InvalidateAllTokens(r.Context(), invalidateParams); mutationErr != nil {
					return sqlc.User{}, mutationErr
				}
			}
			if locked.MustChangePassword {
				if mutationErr = q.ClearMustChangePassword(r.Context(), userID); mutationErr != nil {
					return sqlc.User{}, mutationErr
				}
			}
			return locked, nil
		},
		func() (sqlc.User, error) {
			if mutationErr := h.rehasher.UpdateUserPasswordHash(r.Context(), passwordParams); mutationErr != nil {
				return sqlc.User{}, mutationErr
			}
			if h.revocation != nil {
				if mutationErr := h.revocation.InvalidateAllTokens(r.Context(), invalidateParams); mutationErr != nil {
					return sqlc.User{}, mutationErr
				}
			}
			if dbUser.MustChangePassword {
				if mutationErr := h.rehasher.ClearMustChangePassword(r.Context(), userID); mutationErr != nil && h.log != nil {
					h.log.Warn("failed to clear must_change_password flag", "user_id", userID.String(), "error", mutationErr)
				}
			}
			return dbUser, nil
		},
		func(user sqlc.User) clusterAuditEvent {
			return clusterAuditEvent{
				action: "auth.change_password", resourceType: "user", resourceID: user.ID.String(), resourceName: user.Username,
				status: http.StatusOK, detail: map[string]any{"sessions_invalidated": h.revocation != nil},
			}
		})
	if err != nil {
		if errors.Is(err, errCurrentPasswordIncorrect) {
			RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Current password is incorrect")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update password and invalidate sessions")
		return
	}
	if h.revocation != nil && h.jwt != nil {
		h.jwt.InvalidateUser(r.Context(), userID)
	}

	RespondJSONUnwrapped(w, http.StatusOK, map[string]any{
		"detail":               "Password updated",
		"must_change_password": false,
	})
}

// CurrentUser handles GET /api/v1/auth/me/.
func (h *AuthHandler) CurrentUser(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}

	dbUser, err := h.queries.GetUserByID(r.Context(), userID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "User not found")
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
// openapi:request-operation postAuthTokens
// openapi:request-operation postSettingsTokens
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
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}

	if h.tokens == nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.NotConfigured, "Token management is not configured")
		return
	}

	var req CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Token name is required")
		return
	}

	plaintext, tokenHash, prefix, err := generateAPIToken()
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenGenerationError, "Failed to generate token")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
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
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.QuotaCheckError, "Failed to evaluate token quota")
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
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError,
				"allowed_cidrs must be a comma-separated list of valid CIDR ranges or IP addresses")

			return
		}
	}

	params := sqlc.CreateAPITokenParams{
		UserID:       userID,
		Name:         req.Name,
		TokenHash:    tokenHash,
		Prefix:       prefix,
		ExpiresAt:    expiresAt,
		Scopes:       scopes,
		AllowedCidrs: allowedCIDRs,
	}
	cidrCount := 0
	if allowedCIDRs != "" {
		cidrCount = strings.Count(allowedCIDRs, ",") + 1
	}
	token, err := executeAuthMutation(r, h,
		func(q AuthMutationTx) (sqlc.ApiToken, error) { return q.CreateAPIToken(r.Context(), params) },
		func() (sqlc.ApiToken, error) { return h.tokens.CreateAPIToken(r.Context(), params) },
		func(token sqlc.ApiToken) clusterAuditEvent {
			return clusterAuditEvent{
				action: "auth.token.create", resourceType: "api_token", resourceID: token.ID.String(), resourceName: token.Name,
				status: http.StatusCreated,
				detail: map[string]any{
					"prefix": token.Prefix, "expires_in_days": req.ExpiresInDays,
					"scopes": req.Scopes, "allowed_cidr_count": cidrCount,
				},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create token")
		return
	}

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
	w.Header().Set("Location", "/api/v1/auth/tokens/"+token.ID.String()+"/")
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
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}

	if h.tokens == nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.NotConfigured, "Token management is not configured")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}

	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))

	tokens, err := h.tokens.ListTokensByUser(r.Context(), sqlc.ListTokensByUserParams{
		UserID: userID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list tokens")
		return
	}

	total, err := h.tokens.CountTokensByUser(r.Context(), userID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count tokens")
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
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}

	if h.tokens == nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.NotConfigured, "Token management is not configured")
		return
	}

	tokenIDStr := chi.URLParam(r, "id")
	tokenID, err := uuid.Parse(tokenIDStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid token ID")
		return
	}

	// Verify the token belongs to the authenticated user.
	token, err := h.tokens.GetAPITokenByID(r.Context(), tokenID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Token not found")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}

	if token.UserID != userID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Token not found")
		return
	}

	_, err = executeAuthMutation(r, h,
		func(q AuthMutationTx) (sqlc.ApiToken, error) {
			return token, q.RevokeAPIToken(r.Context(), tokenID)
		},
		func() (sqlc.ApiToken, error) {
			return token, h.tokens.RevokeAPIToken(r.Context(), tokenID)
		},
		func(token sqlc.ApiToken) clusterAuditEvent {
			return clusterAuditEvent{
				action: "auth.token.revoke", resourceType: "api_token", resourceID: token.ID.String(), resourceName: token.Name,
				status: http.StatusNoContent, detail: map[string]any{"prefix": token.Prefix},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.RevokeError, "Failed to revoke token")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
