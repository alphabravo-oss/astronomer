package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/sessionpolicy"
)

// UserQuerier abstracts the user-related database queries needed by AuthHandler.
// This allows for easy testing with mock implementations.
type UserQuerier interface {
	GetUserByEmail(ctx context.Context, email string) (sqlc.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	UpdateUserLastLogin(ctx context.Context, id uuid.UUID) error
}

// LockoutQuerier resets the failure counter after completed authentication.
// Failed-attempt counting and account locking use AuthMutationTx atomically.
type LockoutQuerier interface {
	ResetFailedLoginCount(ctx context.Context, id uuid.UUID) error
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
	ConsumePasswordResetToken(context.Context, sqlc.ConsumePasswordResetTokenParams) (int64, error)
	DeletePasswordResetTokensForUser(context.Context, uuid.UUID) error
	UpdateUserPassword(context.Context, sqlc.UpdateUserPasswordParams) error
	CreateAPIToken(context.Context, sqlc.CreateAPITokenParams) (sqlc.ApiToken, error)
	RevokeAPIToken(context.Context, uuid.UUID) error
	CreateRefreshSession(context.Context, sqlc.CreateRefreshSessionParams) error
	RotateRefreshSession(context.Context, sqlc.RotateRefreshSessionParams) (string, error)
	RevokeRefreshSessionFamily(context.Context, sqlc.RevokeRefreshSessionFamilyParams) (int64, error)
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

	// preferences is intentionally separate from identity queries: console
	// preferences are a self-service domain with their own typed schema and
	// transactionally audited mutation boundary.
	preferences      UserPreferencesQuerier
	preferencesRunTx userPreferencesRunTxFunc

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
	IsEnrolled(ctx context.Context, userID uuid.UUID) (bool, error)
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

// SetUserPreferences wires the read model and its mandatory audited mutation
// transaction. Production provides the same sqlc Queries/database pair used by
// authentication; tests can exercise the boundary independently.
func (h *AuthHandler) SetUserPreferences(q UserPreferencesQuerier, runTx userPreferencesRunTxFunc) {
	if h != nil {
		h.preferences = q
		h.preferencesRunTx = runTx
	}
}

func (h *AuthHandler) TransactionalAuditWired() bool {
	return h != nil && h.runTx != nil
}

func (h *AuthHandler) recordCredentialAuditAs(r *http.Request, userID pgtype.UUID, action, resourceType, resourceID, resourceName string, detail map[string]any) error {
	if h == nil {
		return audit.ErrMandatoryPersistenceUnavailable
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
// ChangePassword. Credential responses fail closed when audit storage is absent.
func (h *AuthHandler) SetAuditWriter(a AuthAuditWriter) {
	h.audit = a
}

// SetLogger overrides the handler's logger.
func (h *AuthHandler) SetLogger(log *slog.Logger) {
	if log != nil {
		h.log = log
	}
}

// SetLockoutQuerier wires the successful-authentication counter reset.
// Failed attempts always require the transaction runner configured by SetRunTx.
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
	User UserResponse `json:"user"`
}

const browserRefreshCookieMaxAge = int((7 * 24 * time.Hour) / time.Second)

func setBrowserSessionCookies(w http.ResponseWriter, r *http.Request, accessToken, refreshToken string) {
	secure := reqctx.RequestIsHTTPS(r)
	if csrfToken, err := newBrowserCSRFToken(); err == nil {
		http.SetCookie(w, &http.Cookie{
			Name:     auth.CSRFCookieName,
			Value:    csrfToken,
			Path:     "/",
			HttpOnly: false,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})
	}
	if accessToken != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     auth.SessionCookieName,
			Value:    accessToken,
			Path:     "/",
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})
	}
	if refreshToken != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     auth.RefreshCookieName,
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
	secure := reqctx.RequestIsHTTPS(r)
	for _, name := range []string{auth.SessionCookieName, auth.RefreshCookieName, auth.CSRFCookieName} {
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
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	authUser, ok := reqctx.AuthenticatedUser(r.Context())
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
	dbUser, err = executeMutation(r, h.runTx,
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
		func(user sqlc.User) mutationAuditEvent {
			return mutationAuditEvent{
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
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}

	var resp map[string]any
	if user.Resolved {
		resp = map[string]any{
			"id": user.ID, "email": user.Email, "username": user.Username,
			"first_name": user.FirstName, "last_name": user.LastName,
			"is_active": user.IsActive, "is_staff": user.IsStaff, "is_superuser": user.IsSuperuser,
			"must_change_password": user.MustChangePassword,
			"date_joined":          user.DateJoined.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if user.HasLastLogin {
			resp["last_login"] = user.LastLogin.UTC().Format("2006-01-02T15:04:05Z")
		} else {
			resp["last_login"] = nil
		}
	} else {
		dbUser, lookupErr := h.queries.GetUserByID(r.Context(), userID)
		if lookupErr != nil {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "User not found")
			return
		}
		resp = map[string]any{
			"id": dbUser.ID.String(), "email": dbUser.Email, "username": dbUser.Username,
			"first_name": dbUser.FirstName, "last_name": dbUser.LastName,
			"is_active": dbUser.IsActive, "is_staff": dbUser.IsStaff, "is_superuser": dbUser.IsSuperuser,
			"must_change_password": dbUser.MustChangePassword,
			"date_joined":          dbUser.DateJoined.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if dbUser.LastLogin.Valid {
			resp["last_login"] = dbUser.LastLogin.Time.UTC().Format("2006-01-02T15:04:05Z")
		} else {
			resp["last_login"] = nil
		}
	}
	// Superusers have platform-wide authority by definition and therefore do
	// not need a role-binding query to describe their effective access. This is
	// also the common operator-session path polled by the dashboard.
	if user.Resolved && user.IsSuperuser {
		resp["roles"] = emptyRoleResponse()
	} else {
		resp["roles"] = h.collectRoles(r.Context(), userID)
	}

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
	out := emptyRoleResponse()
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

func emptyRoleResponse() map[string]any {
	return map[string]any{
		"global":  []any{},
		"cluster": []any{},
		"project": []any{},
	}
}
