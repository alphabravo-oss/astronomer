package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// SSOQuerier abstracts the database queries the SSO handler needs in order
// to provision/lookup users after a successful OAuth handshake. It also
// embeds auth.GroupSyncQuerier so the callback can drive group-sync
// reconciliation (migration 042) without an extra dependency.
type SSOQuerier interface {
	GetUserByEmail(ctx context.Context, email string) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	CreateUser(ctx context.Context, arg sqlc.CreateUserParams) (sqlc.User, error)
	UpdateUserLastLogin(ctx context.Context, id uuid.UUID) error
	GetDexConnectorByName(ctx context.Context, name string) (sqlc.DexConnector, error)
	auth.GroupSyncQuerier
}

// SSOSessionWriter is the narrow surface the SSO callback uses to persist
// the upstream id_token + end_session_endpoint for the single sign-out
// flow (migration 054). Optional: when nil (e.g. tests / pre-DB
// bootstrap), the callback skips the persistence and Logout degrades to
// "local JWT revoked, no upstream redirect".
type SSOSessionWriter interface {
	InsertSSOSession(ctx context.Context, arg sqlc.InsertSSOSessionParams) error
}

// SSORBACInvalidator narrows the RBAC cache hook the SSO callback uses to
// dump a user's cached bindings after a group-sync run mutated them.
// Optional: tests + degenerate installs pass nil and the call is a no-op.
type SSORBACInvalidator interface {
	Invalidate(userID string)
}

// SSOHandler exposes /api/v1/auth/login/{provider}/ and
// /api/v1/auth/callback/{provider}/ endpoints. Provider configuration is
// loaded into the SSOManager at boot from sso_configurations.
type SSOHandler struct {
	manager   *auth.SSOManager
	queries   SSOQuerier
	jwt       *auth.JWTManager
	frontend  string
	rbacCache SSORBACInvalidator
	now       func() time.Time

	// stateStore is a small in-memory CSRF state store. The token expires
	// after 10 minutes; callbacks must arrive in that window.
	mu     sync.Mutex
	states map[string]ssoState

	// sessionWriter persists the upstream id_token + end_session_endpoint
	// to the sso_sessions table so the Logout endpoint can drive
	// RP-initiated logout against the IdP (migration 054). Optional —
	// when nil, SLO is unavailable and Logout falls back to "local JWT
	// revoked only".
	sessionWriter SSOSessionWriter

	// encryptor wraps the upstream id_token at rest. Required by the
	// session writer path; the writer is silently skipped when this is
	// nil so dev / test stacks without an encryption key still boot.
	encryptor *auth.Encryptor
}

type ssoState struct {
	provider  string
	expiresAt time.Time
}

type signedSSOStateCookie struct {
	Provider  string `json:"provider"`
	State     string `json:"state"`
	ExpiresAt int64  `json:"expires_at"`
}

// NewSSOHandler constructs an SSO handler. frontendURL is used as the
// post-callback redirect target; an empty value falls back to "/".
func NewSSOHandler(manager *auth.SSOManager, queries SSOQuerier, jwt *auth.JWTManager, frontendURL string) *SSOHandler {
	if frontendURL == "" {
		frontendURL = "/"
	}
	return &SSOHandler{
		manager:  manager,
		queries:  queries,
		jwt:      jwt,
		frontend: frontendURL,
		now:      time.Now,
		states:   make(map[string]ssoState),
	}
}

// SetRBACCacheInvalidator wires the user-scoped RBAC cache hook so a
// group-sync run that adds or removes bindings is observable on the
// very next authenticated request, instead of after the cache TTL.
// Idempotent; passing nil disables invalidation.
func (h *SSOHandler) SetRBACCacheInvalidator(inv SSORBACInvalidator) {
	if h == nil {
		return
	}
	h.rbacCache = inv
}

// SetSSOSessionWriter wires the sso_sessions writer used by Callback to
// persist the upstream id_token + end_session_endpoint for the SLO
// flow. Idempotent; passing nil disables SLO and the Logout endpoint
// falls back to "JWT revoked locally only".
func (h *SSOHandler) SetSSOSessionWriter(w SSOSessionWriter) {
	if h == nil {
		return
	}
	h.sessionWriter = w
}

// SetEncryptor wires the Fernet encryptor used to wrap the upstream
// id_token before it lands in sso_sessions. Idempotent; passing nil
// disables SLO persistence (the upstream id_token would otherwise be
// stored plaintext, which is bearer-equivalent — strictly worse than
// just degrading to local-only logout).
func (h *SSOHandler) SetEncryptor(e *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = e
}

// Login redirects the user to the provider's authorization URL after stashing
// a CSRF state value in a short-lived cookie + the in-memory state map.
func (h *SSOHandler) Login(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "sso_not_configured", "Single sign-on is not configured")
		return
	}
	provider := strings.ToLower(chi.URLParam(r, "provider"))
	if provider == "" {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_provider", "Provider is required")
		return
	}
	if !h.manager.HasProvider(provider) {
		RespondRequestError(w, r, http.StatusNotFound, "provider_not_found", fmt.Sprintf("SSO provider %q is not enabled", provider))
		return
	}

	authURL, state, err := h.manager.GetAuthorizationURL(provider)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "sso_error", "Failed to start SSO flow")
		return
	}
	h.rememberState(state, provider)
	cookieValue, err := h.signStateCookie(provider, state)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "sso_error", "Failed to persist SSO state")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "astro_sso_state",
		Value:    cookieValue,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  h.now().Add(10 * time.Minute),
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

// Callback handles the OAuth provider redirect, exchanges the code for tokens,
// fetches user info, provisions/looks-up a user, and finally redirects the
// browser back to the frontend with JWT tokens in the query string.
func (h *SSOHandler) Callback(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "sso_not_configured", "Single sign-on is not configured")
		return
	}
	provider := strings.ToLower(chi.URLParam(r, "provider"))
	if provider == "" {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_provider", "Provider is required")
		return
	}
	if !h.manager.HasProvider(provider) {
		RespondRequestError(w, r, http.StatusNotFound, "provider_not_found", fmt.Sprintf("SSO provider %q is not enabled", provider))
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		RespondRequestError(w, r, http.StatusBadRequest, "sso_provider_error", errParam)
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		RespondRequestError(w, r, http.StatusBadRequest, "sso_invalid_request", "Missing code or state")
		return
	}
	if !h.consumeState(state, provider) {
		RespondRequestError(w, r, http.StatusForbidden, "sso_invalid_state", "OAuth state did not match")
		return
	}
	cookie, err := r.Cookie("astro_sso_state")
	if err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "sso_invalid_state", "OAuth state cookie missing")
		return
	}
	if !h.verifyStateCookie(cookie.Value, provider, state) {
		RespondRequestError(w, r, http.StatusForbidden, "sso_invalid_state", "OAuth state cookie mismatch")
		return
	}
	defer http.SetCookie(w, &http.Cookie{Name: "astro_sso_state", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})

	info, err := h.manager.HandleCallback(r.Context(), provider, code, state)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, "sso_callback_error", err.Error())
		return
	}
	if info == nil || info.Email == "" {
		RespondRequestError(w, r, http.StatusBadRequest, "sso_missing_email", "SSO provider did not return an email address")
		return
	}

	user, provisioned, err := h.findOrCreateUser(r.Context(), info)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "sso_user_error", err.Error())
		return
	}
	if !user.IsActive {
		RespondRequestError(w, r, http.StatusForbidden, "account_disabled", "Account is disabled")
		return
	}

	access, refresh, err := h.jwt.GenerateTokenPair(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "token_error", "Failed to generate token")
		return
	}
	if h.queries != nil {
		_ = h.queries.UpdateUserLastLogin(r.Context(), user.ID)
	}

	// Persist the upstream SSO session so Logout can drive RP-initiated
	// logout against the IdP (migration 054 / NIST 800-53 AC-12). Best-
	// effort: any persistence failure logs through the audit row but
	// must NOT block the login response — the user is already
	// authenticated. The Logout fallback ("JWT revoked locally only")
	// covers the degraded path.
	h.persistSSOSession(r, user.ID, provider, access, info)

	if provisioned {
		recordAuditAs(r, h.queries, pgtype.UUID{Bytes: user.ID, Valid: true},
			"sso.user_provisioned", "user", user.ID.String(), user.Username, map[string]any{
				"provider": provider,
				"email":    user.Email,
			},
		)
	}
	recordAuditAs(r, h.queries, pgtype.UUID{Bytes: user.ID, Valid: true},
		"sso.callback", "user", user.ID.String(), user.Username, map[string]any{
			"provider": provider,
		},
	)

	// Group-claim sync (migration 042). Runs unconditionally on every
	// SSO callback so a re-login picks up freshly-revoked claims.
	// "info" came from a successful OIDC/SAML/LDAP handshake, so
	// claims are available even when the slice is empty (zero groups
	// is a valid signal, not "we didn't ask").
	h.syncGroupsFromClaims(r, user.ID, info)

	setBrowserSessionCookies(w, r, access, refresh)
	target, err := url.Parse(h.frontend)
	if err != nil || target.String() == "" {
		target = &url.URL{Path: "/"}
	}
	q := target.Query()
	q.Set("provider", provider)
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// persistSSOSession stores the upstream id_token + cached end_session
// endpoint into sso_sessions, keyed by the access JWT's JTI. Best-
// effort: every failure short-circuits to an audit row + no-op so the
// login response is unaffected. SLO is degraded ("JWT revoked locally
// only") whenever:
//
//   - sessionWriter or encryptor isn't wired (dev / pre-DB bootstrap);
//   - the upstream id_token is empty (non-OIDC providers — GitHub /
//     Google's userinfo path);
//   - Fernet encryption fails (cipher misconfigured);
//   - the access token can't be re-parsed for its JTI (this should be
//     impossible because we just minted it, but we never trust
//     "shouldn't happen" — we fall through to local-only logout).
//
// Encrypted at rest because the id_token is bearer-equivalent while
// it's valid. Never logged.
func (h *SSOHandler) persistSSOSession(r *http.Request, userID uuid.UUID, provider, accessToken string, info *auth.SSOUserInfo) {
	if h == nil {
		return
	}
	if h.sessionWriter == nil || h.encryptor == nil {
		return
	}
	if info == nil || info.UpstreamIDToken == "" {
		// Non-OIDC provider (GitHub orgs / Google userinfo): no
		// id_token to hint with, so SLO is structurally unavailable.
		// Surface it once so an operator wondering why "logout doesn't
		// kick me out of GitHub" can find the answer in the audit
		// stream.
		recordAuditAs(r, h.queries, pgtype.UUID{Bytes: userID, Valid: true},
			"sso.session_skipped", "user", userID.String(), "", map[string]any{
				"provider": provider,
				"reason":   "no_upstream_id_token",
			})
		return
	}
	// Re-parse the access JWT to recover its JTI + exp. The token is
	// freshly minted by us and just round-tripped through string
	// formatting, so this is guaranteed to validate — but we still
	// degrade gracefully because the alternative is panicking on an
	// "impossible" condition that mutates if the JWT layer ever
	// changes.
	claims, err := h.jwt.ValidateToken(accessToken)
	if err != nil {
		recordAuditAs(r, h.queries, pgtype.UUID{Bytes: userID, Valid: true},
			"sso.session_skipped", "user", userID.String(), "", map[string]any{
				"provider": provider,
				"reason":   "jwt_parse_failed",
			})
		return
	}
	if claims.ID == "" || claims.ExpiresAt == nil {
		return
	}
	cipher, err := h.encryptor.Encrypt(info.UpstreamIDToken)
	if err != nil {
		// Same audit + degrade. The user has a valid JWT; SLO just
		// won't be available for this session.
		recordAuditAs(r, h.queries, pgtype.UUID{Bytes: userID, Valid: true},
			"sso.session_skipped", "user", userID.String(), "", map[string]any{
				"provider": provider,
				"reason":   "encrypt_failed",
			})
		return
	}
	if err := h.sessionWriter.InsertSSOSession(r.Context(), sqlc.InsertSSOSessionParams{
		Jti:                      claims.ID,
		UserID:                   userID,
		ProviderName:             provider,
		UpstreamIDTokenEncrypted: cipher,
		EndSessionEndpoint:       info.EndSessionEndpoint,
		ExpiresAt:                claims.ExpiresAt.Time,
	}); err != nil {
		recordAuditAs(r, h.queries, pgtype.UUID{Bytes: userID, Valid: true},
			"sso.session_skipped", "user", userID.String(), "", map[string]any{
				"provider": provider,
				"reason":   "db_write_failed",
			})
	}
}

// syncGroupsFromClaims fires the migration-042 reconciliation against
// the operator-configured identity_group_mappings table. It MUST NOT
// fail the HTTP response — every error path logs an audit row and
// returns; the user is still authenticated and lands on the frontend.
//
// connector_id is left Invalid (NULL) here because the SSOManager
// indexes providers by name (e.g. "google", "okta") rather than by
// the dex_connectors PK; wildcard mappings still apply. A future
// patch can extend SSOUserInfo with a resolved connector_id once Dex
// is fully replacing direct OIDC providers.
func (h *SSOHandler) syncGroupsFromClaims(r *http.Request, userID uuid.UUID, info *auth.SSOUserInfo) {
	if h == nil || h.queries == nil || info == nil {
		return
	}
	// Resolve a dex_connector row for the SSO provider so
	// connector-scoped group mappings match. Look up by name first
	// (operators usually name their dex_connectors row after the
	// provider type), then fall back to wildcard. A non-existent
	// connector for an enabled SSO path is normal in dev — wildcard
	// mappings still apply.
	connectorID := pgtype.UUID{}
	if info.Provider != "" {
		if c, err := h.queries.GetDexConnectorByName(r.Context(), info.Provider); err == nil {
			connectorID = pgtype.UUID{Bytes: c.ID, Valid: true}
		}
	}
	result, err := auth.SyncUserGroups(
		r.Context(),
		h.queries,
		userID,
		connectorID,
		info.Groups,
		true, // claims are fresh on every SSO callback
	)
	if err != nil {
		recordAuditAs(r, h.queries, pgtype.UUID{Bytes: userID, Valid: true},
			"auth.group_sync.error", "user", userID.String(), "", map[string]any{
				"provider": info.Provider,
				"error":    err.Error(),
			},
		)
		return
	}
	if result.Skipped {
		return
	}
	for _, added := range result.Added {
		recordAuditAs(r, h.queries, pgtype.UUID{}, // system actor
			"auth.group_sync.binding_added", "role_binding", added.BindingID.String(), "",
			map[string]any{
				"user_id":    userID.String(),
				"group_name": added.GroupName,
				"role_id":    added.RoleID.String(),
				"scope":      added.Scope,
				"cluster_id": uuidOrEmpty(added.ClusterID),
				"project_id": uuidOrEmpty(added.ProjectID),
			},
		)
	}
	for _, removed := range result.Removed {
		recordAuditAs(r, h.queries, pgtype.UUID{}, // system actor
			"auth.group_sync.binding_removed", "role_binding", removed.BindingID.String(), "",
			map[string]any{
				"user_id":    userID.String(),
				"role_id":    removed.RoleID.String(),
				"scope":      removed.Scope,
				"cluster_id": uuidOrEmpty(removed.ClusterID),
				"project_id": uuidOrEmpty(removed.ProjectID),
			},
		)
	}
	// Invalidate any cached binding set for this user so the very
	// next authenticated request reflects the post-sync state.
	if (len(result.Added) > 0 || len(result.Removed) > 0) && h.rbacCache != nil {
		h.rbacCache.Invalidate(userID.String())
	}
}

// uuidOrEmpty stringifies a uuid for audit JSON, rendering the zero
// value as "" so the column doesn't show "00000000-..." noise.
func uuidOrEmpty(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// findOrCreateUser returns (user, provisioned, error). provisioned is true
// only when this call inserted a fresh row (so the caller can audit a
// distinct sso.user_provisioned event in addition to sso.callback).
func (h *SSOHandler) findOrCreateUser(ctx context.Context, info *auth.SSOUserInfo) (sqlc.User, bool, error) {
	if h.queries == nil {
		return sqlc.User{}, false, errors.New("user persistence is not configured")
	}
	if user, err := h.queries.GetUserByEmail(ctx, info.Email); err == nil {
		return user, false, nil
	}
	username := info.Username
	if username == "" {
		username = info.Email
	}
	if user, err := h.queries.GetUserByUsername(ctx, username); err == nil {
		return user, false, nil
	}
	// Auto-provision: create a disabled-password user record. The password
	// column is non-null in the schema so we store an empty string with the
	// "!" sentinel — this can never match bcrypt or PBKDF2 verification.
	user, err := h.queries.CreateUser(ctx, sqlc.CreateUserParams{
		Email:       info.Email,
		Username:    username,
		FirstName:   info.FirstName,
		LastName:    info.LastName,
		Password:    "!",
		IsActive:    true,
		IsStaff:     false,
		IsSuperuser: false,
	})
	if err != nil {
		return sqlc.User{}, false, fmt.Errorf("provisioning sso user: %w", err)
	}
	return user, true, nil
}

func (h *SSOHandler) rememberState(state, provider string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.states[state] = ssoState{provider: provider, expiresAt: h.now().Add(10 * time.Minute)}
	h.gcStatesLocked()
}

func (h *SSOHandler) consumeState(state, provider string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	st, ok := h.states[state]
	if !ok {
		return false
	}
	delete(h.states, state)
	if h.now().After(st.expiresAt) {
		return false
	}
	return st.provider == provider
}

func (h *SSOHandler) gcStatesLocked() {
	now := h.now()
	for k, v := range h.states {
		if now.After(v.expiresAt) {
			delete(h.states, k)
		}
	}
}

func (h *SSOHandler) signStateCookie(provider, state string) (string, error) {
	payload := signedSSOStateCookie{
		Provider:  provider,
		State:     state,
		ExpiresAt: h.now().Add(10 * time.Minute).Unix(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := h.cookieMAC(encoded)
	if mac == "" {
		return "", errors.New("jwt manager not configured")
	}
	return encoded + "." + mac, nil
}

func (h *SSOHandler) verifyStateCookie(value, provider, state string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return false
	}
	if want := h.cookieMAC(parts[0]); want == "" || !hmac.Equal([]byte(parts[1]), []byte(want)) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var payload signedSSOStateCookie
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	if payload.Provider != provider || payload.State != state {
		return false
	}
	return payload.ExpiresAt >= h.now().Unix()
}

func (h *SSOHandler) cookieMAC(value string) string {
	if h == nil || h.jwt == nil {
		return ""
	}
	key := h.jwt.SecretKey()
	if len(key) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// SSOConfigQuerier is unused by this handler today but reserved for the
// future REST CRUD ViewSet that another agent will register. We keep the
// alias here so importers have a single place to find it.
type SSOConfigQuerier = auth.SSOConfigQuerier
