package handler

import (
	"context"
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
// to provision/lookup users after a successful OAuth handshake.
type SSOQuerier interface {
	GetUserByEmail(ctx context.Context, email string) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	CreateUser(ctx context.Context, arg sqlc.CreateUserParams) (sqlc.User, error)
	UpdateUserLastLogin(ctx context.Context, id uuid.UUID) error
}

// SSOHandler exposes /api/v1/auth/login/{provider}/ and
// /api/v1/auth/callback/{provider}/ endpoints. Provider configuration is
// loaded into the SSOManager at boot from sso_configurations.
type SSOHandler struct {
	manager  *auth.SSOManager
	queries  SSOQuerier
	jwt      *auth.JWTManager
	frontend string

	// stateStore is a small in-memory CSRF state store. The token expires
	// after 10 minutes; callbacks must arrive in that window.
	mu     sync.Mutex
	states map[string]ssoState
}

type ssoState struct {
	provider  string
	expiresAt time.Time
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
		states:   make(map[string]ssoState),
	}
}

// Login redirects the user to the provider's authorization URL after stashing
// a CSRF state value in a short-lived cookie + the in-memory state map.
func (h *SSOHandler) Login(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		RespondError(w, http.StatusServiceUnavailable, "sso_not_configured", "Single sign-on is not configured")
		return
	}
	provider := strings.ToLower(chi.URLParam(r, "provider"))
	if provider == "" {
		RespondError(w, http.StatusBadRequest, "invalid_provider", "Provider is required")
		return
	}
	if !h.manager.HasProvider(provider) {
		RespondError(w, http.StatusNotFound, "provider_not_found", fmt.Sprintf("SSO provider %q is not enabled", provider))
		return
	}

	authURL, state, err := h.manager.GetAuthorizationURL(provider)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "sso_error", "Failed to start SSO flow")
		return
	}
	h.rememberState(state, provider)
	http.SetCookie(w, &http.Cookie{
		Name:     "astro_sso_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(10 * time.Minute),
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

// Callback handles the OAuth provider redirect, exchanges the code for tokens,
// fetches user info, provisions/looks-up a user, and finally redirects the
// browser back to the frontend with JWT tokens in the query string.
func (h *SSOHandler) Callback(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		RespondError(w, http.StatusServiceUnavailable, "sso_not_configured", "Single sign-on is not configured")
		return
	}
	provider := strings.ToLower(chi.URLParam(r, "provider"))
	if provider == "" {
		RespondError(w, http.StatusBadRequest, "invalid_provider", "Provider is required")
		return
	}
	if !h.manager.HasProvider(provider) {
		RespondError(w, http.StatusNotFound, "provider_not_found", fmt.Sprintf("SSO provider %q is not enabled", provider))
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		RespondError(w, http.StatusBadRequest, "sso_provider_error", errParam)
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		RespondError(w, http.StatusBadRequest, "sso_invalid_request", "Missing code or state")
		return
	}
	if !h.consumeState(state, provider) {
		RespondError(w, http.StatusForbidden, "sso_invalid_state", "OAuth state did not match")
		return
	}
	if cookie, err := r.Cookie("astro_sso_state"); err == nil {
		// Best-effort cookie validation; mismatch is reported as a security failure.
		if cookie.Value != state {
			RespondError(w, http.StatusForbidden, "sso_invalid_state", "OAuth state cookie mismatch")
			return
		}
	}

	info, err := h.manager.HandleCallback(r.Context(), provider, code, state)
	if err != nil {
		RespondError(w, http.StatusBadGateway, "sso_callback_error", err.Error())
		return
	}
	if info == nil || info.Email == "" {
		RespondError(w, http.StatusBadRequest, "sso_missing_email", "SSO provider did not return an email address")
		return
	}

	user, provisioned, err := h.findOrCreateUser(r.Context(), info)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "sso_user_error", err.Error())
		return
	}
	if !user.IsActive {
		RespondError(w, http.StatusForbidden, "account_disabled", "Account is disabled")
		return
	}

	access, refresh, err := h.jwt.GenerateTokenPair(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "token_error", "Failed to generate token")
		return
	}
	if h.queries != nil {
		_ = h.queries.UpdateUserLastLogin(r.Context(), user.ID)
	}

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

	target, err := url.Parse(h.frontend)
	if err != nil || target.String() == "" {
		target = &url.URL{Path: "/"}
	}
	q := target.Query()
	q.Set("token", access)
	q.Set("refresh", refresh)
	q.Set("provider", provider)
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
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
	h.states[state] = ssoState{provider: provider, expiresAt: time.Now().Add(10 * time.Minute)}
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
	if time.Now().After(st.expiresAt) {
		return false
	}
	return st.provider == provider
}

func (h *SSOHandler) gcStatesLocked() {
	now := time.Now()
	for k, v := range h.states {
		if now.After(v.expiresAt) {
			delete(h.states, k)
		}
	}
}

// SSOConfigQuerier is unused by this handler today but reserved for the
// future REST CRUD ViewSet that another agent will register. We keep the
// alias here so importers have a single place to find it.
type SSOConfigQuerier = auth.SSOConfigQuerier
