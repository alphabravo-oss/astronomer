package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/identity"
)

type SSORBACInvalidator interface{ Invalidate(string) }

type SSOCallbackTx interface {
	audit.OutboxQuerier
	identityBindingQueries
	UpdateUserLastLogin(context.Context, uuid.UUID) error
}

type SSOHandler struct {
	service *identity.Service
	jwt     *auth.JWTManager
	runTx   func(context.Context, func(SSOCallbackTx) error) error
}

func NewSSOHandler(service *identity.Service, manager *auth.JWTManager, _ string) *SSOHandler {
	return &SSOHandler{service: service, jwt: manager}
}
func (h *SSOHandler) SetRunTx(fn func(context.Context, func(SSOCallbackTx) error) error) {
	h.runTx = fn
}
func (h *SSOHandler) TransactionalAuditWired() bool {
	return h != nil && h.service != nil && h.jwt != nil && h.runTx != nil
}
func (h *SSOHandler) ListProviders(w http.ResponseWriter, r *http.Request) {
	providers := []map[string]any{}
	if h != nil && h.service.Enabled() {
		providers = append(providers, map[string]any{"id": uuid.NewSHA1(uuid.NameSpaceURL, []byte(h.service.Issuer())).String(), "provider": "identity", "type": "oidc", "name": "Single sign-on", "enabled": true})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(providers)
}
func (h *SSOHandler) Login(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	web, err := h.service.Browser(r.Context())
	if err != nil {
		apiutil.WriteError(w, r, 503, apiutil.SSONotConfigured, "Single sign-on is temporarily unavailable")
		return
	}
	web.Login(w, r)
}
func (h *SSOHandler) available(w http.ResponseWriter, r *http.Request) bool {
	provider := chi.URLParam(r, "provider")
	if (provider != "" && provider != "identity") || h == nil || !h.service.Enabled() {
		apiutil.WriteError(w, r, 404, apiutil.SSONotConfigured, "Single sign-on is not configured")
		return false
	}
	return true
}

// Callback delegates code/PKCE/state verification to the shared library. Its
// provider token is consumed on the server and never sent to the browser.
func (h *SSOHandler) Callback(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	if !h.TransactionalAuditWired() {
		apiutil.WriteError(w, r, 503, apiutil.SSOError, "Single sign-on persistence is unavailable")
		return
	}
	web, err := h.service.Browser(r.Context())
	if err != nil {
		apiutil.WriteError(w, r, 503, apiutil.SSOError, "Single sign-on is temporarily unavailable")
		return
	}
	result := &oidcResponse{header: make(http.Header)}
	web.Callback(result, r)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	response := http.Response{Header: result.header}
	raw := ""
	for _, cookie := range response.Cookies() {
		if cookie.Name == identity.BrowserCookie && cookie.MaxAge >= 0 {
			raw = cookie.Value
		} else if cookie.Name == identity.BrowserCookie+".flow" {
			http.SetCookie(w, cookie)
		}
	}
	if result.status != http.StatusSeeOther || raw == "" {
		apiutil.WriteError(w, r, 401, apiutil.SSOCallbackError, "Single sign-on failed")
		return
	}
	user, err := h.service.Authenticate(r.Context(), raw)
	if err != nil {
		apiutil.WriteError(w, r, 403, apiutil.SSOUserError, "External identity is not linked to an active account")
		return
	}
	// These exact bytes have already passed the shared cryptographic verifier.
	var claims jwt.RegisteredClaims
	if _, _, err := jwt.NewParser().ParseUnverified(raw, &claims); err != nil || claims.ExpiresAt == nil {
		apiutil.WriteError(w, r, 401, apiutil.SSOCallbackError, "Invalid identity token")
		return
	}
	access, err := h.jwt.GenerateIdentitySession(r.Context(), user.ID, h.service.Issuer(), claims.Subject, claims.ExpiresAt.Time)
	if err == nil {
		err = h.runTx(r.Context(), func(q SSOCallbackTx) error {
			if err := q.UpdateUserLastLogin(r.Context(), user.ID); err != nil {
				return err
			}
			return recordAuditOutboxAs(r, q, pgtype.UUID{Bytes: user.ID, Valid: true}, "sso.callback", "user", user.ID.String(), user.Username, http.StatusSeeOther, map[string]any{"provider": "identity"})
		})
	}
	if err != nil {
		apiutil.WriteError(w, r, 503, apiutil.SSOError, "Single sign-on persistence is unavailable")
		return
	}
	clearBrowserSessionCookies(w, r)
	setBrowserSessionCookies(w, r, access, "")
	http.Redirect(w, r, "/dashboard/", http.StatusSeeOther)
}

type oidcResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *oidcResponse) Header() http.Header { return r.header }
func (r *oidcResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}
func (r *oidcResponse) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	if r.body.Len() < 4096 {
		_, _ = r.body.Write(b[:min(len(b), 4096-r.body.Len())])
	}
	return len(b), nil
}
func uuidOrEmpty(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return strings.TrimSpace(id.String())
}
