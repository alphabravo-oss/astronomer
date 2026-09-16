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
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
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
	ClaimExternalPrincipal(ctx context.Context, arg sqlc.ClaimExternalPrincipalParams) (sqlc.User, error)
	auth.GroupSyncQuerier
}

// SSORBACInvalidator narrows the RBAC cache hook the SSO callback uses to
// dump a user's cached bindings after a group-sync run mutated them.
// Required so committed claim revocations take effect immediately.
type SSORBACInvalidator interface {
	Invalidate(userID string)
}

type ssoFlowManager interface {
	HasProvider(name string) bool
	GetAuthorizationURL(provider string) (url string, state string, err error)
	HandleCallback(ctx context.Context, provider, code, state string) (*auth.SSOUserInfo, error)
}

// SSOHandler exposes /api/v1/auth/login/{provider}/ and
// /api/v1/auth/callback/{provider}/ endpoints. Provider configuration is
// loaded into the SSOManager at boot from sso_configurations.
type SSOHandler struct {
	manager   ssoFlowManager
	jwt       *auth.JWTManager
	frontend  string
	rbacCache SSORBACInvalidator
	now       func() time.Time

	// stateStore is a small in-memory CSRF state store. The token expires
	// after 10 minutes; callbacks must arrive in that window.
	mu     sync.Mutex
	states map[string]ssoState

	runTx     ssoRunTxFunc
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
func NewSSOHandler(manager *auth.SSOManager, jwt *auth.JWTManager, frontendURL string) *SSOHandler {
	if frontendURL == "" {
		frontendURL = "/"
	}
	var flow ssoFlowManager
	if manager != nil {
		flow = manager
	}
	return &SSOHandler{
		manager:  flow,
		jwt:      jwt,
		frontend: frontendURL,
		now:      time.Now,
		states:   make(map[string]ssoState),
	}
}

// SetRBACCacheInvalidator wires the user-scoped RBAC cache hook so a
// group-sync run that adds or removes bindings is observable on the
// very next authenticated request, instead of after the cache TTL.
// Passing nil makes callbacks fail closed.
func (h *SSOHandler) SetRBACCacheInvalidator(inv SSORBACInvalidator) {
	if h == nil {
		return
	}
	h.rbacCache = inv
}

// SetEncryptor wires the required upstream-token encryption dependency.
func (h *SSOHandler) SetEncryptor(e *auth.Encryptor) {
	if h != nil {
		h.encryptor = e
	}
}

// Login redirects the user to the provider's authorization URL after stashing
// a CSRF state value in a short-lived cookie + the in-memory state map.
func (h *SSOHandler) Login(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.SSONotConfigured, "Single sign-on is not configured")
		return
	}
	provider := strings.ToLower(chi.URLParam(r, "provider"))
	if provider == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidProvider, "Provider is required")
		return
	}
	if !h.manager.HasProvider(provider) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, fmt.Sprintf("SSO provider %q is not enabled", provider))
		return
	}

	authURL, state, err := h.manager.GetAuthorizationURL(provider)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SSOError, "Failed to start SSO flow")
		return
	}
	h.rememberState(state, provider)
	cookieValue, err := h.signStateCookie(provider, state)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SSOError, "Failed to persist SSO state")
		return
	}
	http.SetCookie(w, ssoStateCookie(provider, cookieValue, 10*time.Minute, h.now()))
	http.Redirect(w, r, authURL, http.StatusFound)
}

// Callback handles the OAuth provider redirect, exchanges the code for tokens,
// fetches user info, provisions/looks-up a user, and finally redirects the
// browser back to the frontend with secure session cookies.
func (h *SSOHandler) Callback(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.SSONotConfigured, "Single sign-on is not configured")
		return
	}
	provider := strings.ToLower(chi.URLParam(r, "provider"))
	if provider == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidProvider, "Provider is required")
		return
	}
	if !h.manager.HasProvider(provider) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, fmt.Sprintf("SSO provider %q is not enabled", provider))
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.SSOProviderError, errParam)
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.SSOInvalidRequest, "Missing code or state")
		return
	}
	// CSRF protection is enforced by the HMAC-signed `astro_sso_state`
	// cookie verified below, which is stateless and therefore HA-safe: a
	// Login served by replica A and a Callback served by replica B (behind
	// a round-robin LB) both validate against the same JWT signing key.
	// The in-memory state map is consulted best-effort only — it is local
	// to a single process, so requiring a hit here breaks SSO in any
	// multi-replica deployment. We consume it (for single-process replay
	// detection + GC) but never fail the callback on a miss; the signed
	// cookie is the authority.
	h.consumeState(state, provider)
	cookie, err := r.Cookie("astro_sso_state")
	if err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.SSOInvalidState, "OAuth state cookie missing")
		return
	}
	if !h.verifyStateCookie(cookie.Value, provider, state) {
		RespondRequestError(w, r, http.StatusForbidden, apierror.SSOInvalidState, "OAuth state cookie mismatch")
		return
	}
	defer http.SetCookie(w, ssoStateCookie(provider, "", -1, h.now()))

	info, err := h.manager.HandleCallback(r.Context(), provider, code, state)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.SSOCallbackError, err.Error())
		return
	}
	if info == nil || info.Email == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.SSOMissingEmail, "SSO provider did not return an email address")
		return
	}

	user, pair, err := h.commitSSOCallback(r, provider, info)
	if err != nil {
		if errors.Is(err, errSSOAccountDisabled) {
			RespondRequestError(w, r, http.StatusForbidden, apierror.AccountDisabled, "Account is disabled")
		} else {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.SSOUserError, "Single sign-on persistence is unavailable")
		}
		return
	}
	access, refresh, err := h.jwt.SignPreparedTokenPair(user.ID, pair)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.TokenError, "Failed to generate token")
		return
	}

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

func ssoStateCookie(provider, value string, lifetime time.Duration, now time.Time) *http.Cookie {
	path := "/api/v1/auth/login/" + strings.ToLower(strings.TrimSpace(provider)) + "/callback"
	cookie := &http.Cookie{
		Name:     "astro_sso_state",
		Value:    value,
		Path:     path,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
	if lifetime < 0 {
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(1, 0).UTC()
		return cookie
	}
	cookie.MaxAge = int(lifetime / time.Second)
	cookie.Expires = now.Add(lifetime)
	return cookie
}

// uuidOrEmpty stringifies a uuid for audit JSON, rendering the zero
// value as "" so the column doesn't show "00000000-..." noise.
func uuidOrEmpty(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// findOrCreateSSOUser returns (user, provisioned, linked, error). provisioned is true
// only when this call inserted a fresh row (so the caller can audit a
// distinct sso.user_provisioned event in addition to sso.callback). linked is
// true when a pre-materialized external principal was claimed atomically.
func findOrCreateSSOUser(ctx context.Context, q SSOQuerier, info *auth.SSOUserInfo) (sqlc.User, bool, bool, error) {
	if q == nil {
		return sqlc.User{}, false, false, errors.New("user persistence is not configured")
	}
	email := strings.ToLower(strings.TrimSpace(info.Email))
	username := strings.TrimSpace(info.Username)
	if username == "" {
		username = email
	}
	if info.ConnectorID != "" && info.Subject != "" {
		displayName := strings.TrimSpace(info.FirstName + " " + info.LastName)
		user, err := q.ClaimExternalPrincipal(ctx, sqlc.ClaimExternalPrincipalParams{
			PrincipalEmail:       email,
			PrincipalUsername:    username,
			PrincipalDisplayName: displayName,
			TargetConnectorName:  info.ConnectorID,
			TargetSubject:        info.Subject,
		})
		if err == nil {
			return user, false, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return sqlc.User{}, false, false, fmt.Errorf("claiming external principal: %w", err)
		}
	}
	if user, err := q.GetUserByEmail(ctx, email); err == nil {
		return user, false, false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.User{}, false, false, fmt.Errorf("looking up sso user by email: %w", err)
	}
	if user, err := q.GetUserByUsername(ctx, username); err == nil {
		return user, false, false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.User{}, false, false, fmt.Errorf("looking up sso user by username: %w", err)
	}
	// Auto-provision: create a disabled-password user record. The password
	// column is non-null in the schema so we store an empty string with the
	// "!" sentinel — this can never match bcrypt or PBKDF2 verification.
	user, err := q.CreateUser(ctx, sqlc.CreateUserParams{
		Email:       email,
		Username:    username,
		FirstName:   info.FirstName,
		LastName:    info.LastName,
		Password:    "!",
		IsActive:    true,
		IsStaff:     false,
		IsSuperuser: false,
	})
	if err != nil {
		return sqlc.User{}, false, false, fmt.Errorf("provisioning sso user: %w", err)
	}
	return user, true, false, nil
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
