package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/sessionpolicy"
)

func (h *MonitoringHandler) SetGrafanaTickets(store *auth.GrafanaTicketStore) {
	if h == nil {
		return
	}
	h.grafanaTickets = store
}

func (h *MonitoringHandler) SetUserLookup(users UserByIDQuerier) {
	if h == nil {
		return
	}
	h.users = users
}

func (h *MonitoringHandler) SetServerURL(serverURL string) {
	if h == nil {
		return
	}
	h.serverURL = strings.TrimSpace(serverURL)
}

func (h *MonitoringHandler) SetGrafanaProxyImage(image string) {
	if h == nil {
		return
	}
	h.proxyImage = strings.TrimSpace(image)
}

func (h *MonitoringHandler) SetGrafanaExpose(expose GrafanaExpose) {
	if h == nil {
		return
	}
	h.grafanaExpose = GrafanaExpose{
		GatewayClass:      strings.TrimSpace(expose.GatewayClass),
		IngressClass:      strings.TrimSpace(expose.IngressClass),
		GatewayName:       strings.TrimSpace(expose.GatewayName),
		PlatformNamespace: strings.TrimSpace(expose.PlatformNamespace),
		TLSIssuerName:     strings.TrimSpace(expose.TLSIssuerName),
		TLSIssuerKind:     strings.TrimSpace(expose.TLSIssuerKind),
	}
}

func (h *MonitoringHandler) SetSessionTTL(fn func(context.Context) time.Duration) {
	if h == nil {
		return
	}
	h.sessionTTL = fn
}

// MintGrafanaTicket is GET /api/v1/observability/grafana-ticket?return=.
// Session + monitoring:read (any scope). Allow-lists return. Does not log
// ticket or return.
func (h *MonitoringHandler) MintGrafanaTicket(w http.ResponseWriter, r *http.Request) {
	if !h.authz.authorizeAnyScope(w, r, rbac.ResourceMonitoring, rbac.VerbRead) {
		return
	}
	if h == nil || h.grafanaTickets == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Grafana tickets are not configured")
		return
	}
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || user == nil || user.ID == "" {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Invalid authenticated user")
		return
	}

	grafanaHost := h.resolvedGrafanaHost(r)
	returnURL, err := allowListedGrafanaReturn(r.URL.Query().Get("return"), grafanaHost, publicScheme(h.serverURL))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "return URL is not allow-listed")
		return
	}

	email, role, explore, admin := h.grafanaIdentity(r, user)
	if email == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "authenticated user has no email")
		return
	}
	token, _, err := h.grafanaTickets.Issue(userID, email, role, explore, admin, h.grafanaCookieTTL(r.Context()))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TicketError, "Failed to issue Grafana ticket")
		return
	}
	// Do not log ticket or return.
	if h.log != nil {
		h.log.Info("grafana ticket minted", "user_id", userID.String(), "role", role)
	}
	http.Redirect(w, r, appendTicketQuery(returnURL, token), http.StatusFound)
}

type grafanaTicketRedeemRequest struct {
	Ticket string `json:"ticket"`
}

// RedeemGrafanaTicket is POST /api/v1/observability/grafana-ticket/redeem.
// The ticket is the only credential. 401 if missing/used/expired.
func (h *MonitoringHandler) RedeemGrafanaTicket(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.grafanaTickets == nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "ticket is missing, used, or expired")
		return
	}
	var req grafanaTicketRedeemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "ticket is missing")
		return
	}
	ticket, err := h.grafanaTickets.Take(req.Ticket)
	if err != nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "ticket is missing, used, or expired")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"email":   ticket.Email,
		"role":    ticket.Role,
		"ttl":     ticket.CookieTTL,
		"explore": ticket.Explore,
		"admin":   ticket.Admin,
	})
}

func (h *MonitoringHandler) grafanaIdentity(r *http.Request, session *middleware.AuthenticatedUser) (email, role string, explore, admin bool) {
	email = strings.TrimSpace(session.Email)
	isSuperuser := false
	if h.users != nil {
		if user, err := authenticatedUserFromRequest(r, h.users); err == nil {
			if user.Email != "" {
				email = user.Email
			}
			isSuperuser = user.IsSuperuser
		}
	}
	if isSuperuser {
		return email, "Admin", true, true
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		return email, "Viewer", false, false
	}
	if !restricted || h.authz.allowsGlobal(bindings, rbac.ResourceMonitoring, rbac.VerbUpdate) {
		return email, "Editor", true, false
	}
	return email, "Viewer", false, false
}

func (h *MonitoringHandler) resolvedGrafanaHost(r *http.Request) string {
	if h != nil && h.queries != nil && r != nil {
		if backend, err := h.queries.GetDefaultMonitoringBackend(r.Context()); err == nil {
			meta := sharedStackMetadata(backend, "sharedGrafana")
			if host := strings.TrimSpace(stringFromMap(meta, "grafanaHost")); host != "" {
				return stripHostScheme(host)
			}
			if host := strings.TrimSpace(stringFromMap(meta, "ingressHost")); host != "" {
				return stripHostScheme(host)
			}
		}
	}
	if h != nil {
		return defaultGrafanaHost(h.serverURL)
	}
	return ""
}

func defaultGrafanaHost(serverURL string) string {
	host := hostnameOf(serverURL)
	if host == "" {
		return ""
	}
	return "grafana." + host
}

func hostnameOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return u.Hostname()
}

func stripHostScheme(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimSuffix(host, "/")
	return host
}

// allowListedGrafanaReturn accepts only <scheme>://<grafanaHost>/ or
// /auth/callback. Scheme matches the Astronomer public URL (https unless
// ServerURL is http). No open redirect, no //, no other hosts.
func allowListedGrafanaReturn(raw, grafanaHost, scheme string) (string, error) {
	raw = strings.TrimSpace(raw)
	grafanaHost = stripHostScheme(grafanaHost)
	if scheme == "" {
		scheme = "https"
	}
	if raw == "" || grafanaHost == "" {
		return "", errGrafanaReturnRejected
	}
	if strings.ContainsAny(raw, "\n\r\\") {
		return "", errGrafanaReturnRejected
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() {
		return "", errGrafanaReturnRejected
	}
	if !strings.EqualFold(u.Scheme, scheme) {
		return "", errGrafanaReturnRejected
	}
	if u.User != nil {
		return "", errGrafanaReturnRejected
	}
	if !strings.EqualFold(u.Host, grafanaHost) {
		return "", errGrafanaReturnRejected
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if path != "/" && path != "/auth/callback" {
		return "", errGrafanaReturnRejected
	}
	if strings.Contains(path, "//") {
		return "", errGrafanaReturnRejected
	}
	return (&url.URL{Scheme: scheme, Host: grafanaHost, Path: path}).String(), nil
}

func appendTicketQuery(returnURL, ticket string) string {
	u, err := url.Parse(returnURL)
	if err != nil {
		return returnURL
	}
	q := u.Query()
	q.Set("ticket", ticket)
	u.RawQuery = q.Encode()
	return u.String()
}

func (h *MonitoringHandler) grafanaCookieTTL(ctx context.Context) time.Duration {
	if h != nil && h.sessionTTL != nil {
		if d := h.sessionTTL(ctx); d > 0 {
			return d
		}
	}
	return time.Duration(sessionpolicy.DefaultMinutes) * time.Minute
}

func publicScheme(serverURL string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(serverURL)), "http://") {
		return "http"
	}
	return "https"
}

var errGrafanaReturnRejected = errors.New("grafana return URL rejected")
