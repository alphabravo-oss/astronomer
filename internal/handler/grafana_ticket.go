package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
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

// openapi:request GrafanaTicketRedeemRequest
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
		"email":      ticket.Email,
		"role":       ticket.Role,
		"ttl":        ticket.CookieTTL,
		"explore":    ticket.Explore,
		"admin":      ticket.Admin,
		"clusterIds": ticket.ClusterIDs,
	})
}

func (h *MonitoringHandler) grafanaIdentity(r *http.Request, session *reqctx.User) (email, role string, explore, admin bool, clusterIDs []string) {
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
		return email, "Admin", true, true, nil
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		return email, "Viewer", false, false, nil
	}
	if !restricted || h.authz.allowsGlobal(bindings, rbac.ResourceMonitoring, rbac.VerbUpdate) {
		return email, "Editor", true, false, nil
	}
	all, ids, _, err := h.authz.authorizedScopeIDs(r.Context(), rbac.ResourceMonitoring, rbac.VerbRead, rbac.NarrowedClustersWiden)
	if err != nil || all {
		return email, "Viewer", false, false, nil
	}
	clusterIDs = uuidStrings(ids)
	// Cluster-scoped Explore is reopened only when rewrite has cluster IDs to inject.
	explore = len(clusterIDs) > 0
	return email, "Viewer", explore, false, clusterIDs
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
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

func (h *MonitoringHandler) grafanaCookieTTL(ctx context.Context) time.Duration {
	if h != nil && h.sessionTTL != nil {
		if d := h.sessionTTL(ctx); d > 0 {
			return d
		}
	}
	return time.Duration(sessionpolicy.DefaultMinutes) * time.Minute
}
