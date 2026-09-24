package handler

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/grafanaproxy"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// ProxyClusterGrafana rechecks cluster RBAC and issues a one-use identity ticket
// on every request. Browser cookies or headers cannot select the upstream user.
func (h *MonitoringHandler) ProxyClusterGrafana(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok || user == nil || user.ID == "" {
		RespondRequestError(w, r, 401, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceMonitoring, rbac.VerbRead) {
		return
	}
	requester, ok := h.requester.(grafanaAuthenticatedRequester)
	if !ok || h.queries == nil || h.grafanaTickets == nil {
		RespondRequestError(w, r, 503, apierror.Unavailable, "Cluster Grafana proxy is not configured")
		return
	}
	cfg, err := h.queries.GetClusterMonitoringConfig(r.Context(), clusterID)
	if err != nil || !grafanaStackPresent(cfg.Status) {
		RespondRequestError(w, r, 404, apierror.NotFound, "Cluster Grafana is not installed")
		return
	}
	service := grafanaProxyServiceName(cfg.PrometheusReleaseName)
	if !isSafeK8sName(service) || !isSafeK8sName(cfg.StackNamespace) {
		RespondRequestError(w, r, 503, apierror.Unavailable, "Cluster Grafana target is invalid")
		return
	}
	upstream := r.Clone(r.Context())
	upstream.URL.Path = "/" + strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	upstream.URL.RawPath = ""
	identity := h.clusterGrafanaIdentity(r, user, clusterID)
	upstream, status, err := grafanaproxy.PrepareRequest(upstream, identity)
	if err != nil {
		RespondRequestError(w, r, 400, apierror.ValidationError, "Invalid Grafana query")
		return
	}
	if status != 0 {
		RespondRequestError(w, r, status, apierror.Forbidden, http.StatusText(status))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, upstream.Body, serviceProxyMaxBodyBytes))
	if err != nil {
		RespondRequestError(w, r, 413, apierror.InvalidBody, "Grafana request exceeds maximum allowed size")
		return
	}
	uid, err := uuid.Parse(user.ID)
	if err != nil || identity.Email == "" {
		RespondRequestError(w, r, 401, apierror.AuthenticationRequired, "Authenticated user is invalid")
		return
	}
	ticket, _, err := h.grafanaTickets.Issue(uid, identity.Email, identity.Role, identity.Explore, identity.Admin, h.grafanaCookieTTL(r.Context()), nil)
	if err != nil {
		RespondRequestError(w, r, 500, apierror.TicketError, "Failed to authorize Grafana request")
		return
	}
	prefix := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:%d/proxy", cfg.StackNamespace, service, grafanaProxyListenPort)
	path := prefix + upstream.URL.RequestURI()
	resp, err := requester.DoWithGrafanaAuth(r.Context(), clusterID.String(), r.Method, path, body, grafanaRequestHeaders(upstream.Header), protocol.GrafanaProxyAuth{Ticket: ticket})
	if err != nil || resp == nil {
		RespondRequestError(w, r, 503, apierror.ProxyError, "Cluster Grafana is unavailable")
		return
	}
	if isServiceProxyAuditMethod(r.Method) {
		recordAudit(r, h.queries, "monitoring.cluster_grafana.proxy", "cluster", clusterID.String(), service, map[string]any{"namespace": cfg.StackNamespace, "method": r.Method, "path": upstream.URL.Path})
	}
	writeGrafanaResponse(w, r, resp, prefix)
}

func (h *MonitoringHandler) clusterGrafanaIdentity(r *http.Request, user *reqctx.User, clusterID uuid.UUID) grafanaproxy.Identity {
	email, role, _, admin, _ := h.grafanaIdentity(r, user)
	if !admin {
		role = "Viewer"
		bindings, restricted, err := h.authz.bindingsForContext(r.Context())
		if err == nil && (!restricted || h.authz.allowsCluster(bindings, clusterID, rbac.ResourceMonitoring, rbac.VerbUpdate)) {
			role = "Editor"
		}
	}
	// This data source is physically confined to the already-authorized cluster;
	// its local metrics and logs do not carry shared-storage cluster_id labels.
	return grafanaproxy.Identity{Email: email, Role: role, Explore: true, Admin: admin}
}
