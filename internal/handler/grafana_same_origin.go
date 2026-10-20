package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/grafanaproxy"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const grafanaProxyCSP = "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'self'; img-src 'self' data: blob:; font-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; connect-src 'self' ws: wss:; worker-src 'self' blob:"

type grafanaAuthenticatedRequester interface {
	DoWithGrafanaAuth(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string, auth protocol.GrafanaProxyAuth) (*protocol.K8sResponsePayload, error)
}

// ProxyGrafana is mounted only behind the isolated tool gateway.
// Authorization is evaluated on every request, and scoped
// users have datasource queries rewritten to their allowed cluster IDs before
// the request enters the tunnel.
func (h *MonitoringHandler) ProxyGrafana(w http.ResponseWriter, r *http.Request) {
	if !h.authz.authorizeAnyScope(w, r, rbac.ResourceMonitoring, rbac.VerbRead) {
		return
	}
	requester, ok := h.requester.(grafanaAuthenticatedRequester)
	if !ok || h.queries == nil || h.grafanaTickets == nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.Unavailable, "Grafana proxy is not configured")
		return
	}
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok || user == nil || user.ID == "" {
		apiutil.WriteError(w, r, http.StatusUnauthorized, apiutil.AuthenticationRequired, "Authentication required")
		return
	}

	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.Unavailable, "Shared Grafana is not configured")
		return
	}
	metadata := sharedStackMetadata(backend, "sharedGrafana")
	if !grafanaStackPresent(stringFromMap(metadata, "status")) {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Shared Grafana is not installed")
		return
	}
	clusterID := strings.TrimSpace(stringFromMap(metadata, "managementClusterId"))
	namespace := defaultString(stringFromMap(metadata, "namespace"), "monitoring")
	release := defaultString(stringFromMap(metadata, "releaseName"), sharedGrafanaDefaultRelease)
	service := grafanaProxyServiceName(release)
	if _, err := uuid.Parse(clusterID); err != nil || !isSafeK8sName(namespace) || !isSafeK8sName(service) {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.Unavailable, "Shared Grafana target is invalid")
		return
	}

	upstreamRequest := r.Clone(r.Context())
	suffix := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	upstreamRequest.URL.Path = "/" + suffix
	upstreamRequest.URL.RawPath = ""
	email, role, explore, admin, clusterIDs := h.grafanaIdentity(r, user)
	if email == "" {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Authenticated user has no email")
		return
	}
	upstreamRequest, status, err := grafanaproxy.PrepareRequest(upstreamRequest, grafanaproxy.Identity{
		Email: email, Role: role, Explore: explore, Admin: admin, ClusterIDs: clusterIDs,
	})
	if err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Invalid Grafana query")
		return
	}
	if status != 0 {
		apiutil.WriteError(w, r, status, apiutil.Forbidden, http.StatusText(status))
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, upstreamRequest.Body, serviceProxyMaxBodyBytes))
	if err != nil {
		apiutil.WriteError(w, r, http.StatusRequestEntityTooLarge, apiutil.InvalidBody, "Grafana request exceeds maximum allowed size")
		return
	}
	auth, err := h.grafanaProxyAuth(r, user, email, role, explore, admin, clusterIDs)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.TicketError, "Failed to authorize Grafana request")
		return
	}

	proxyPath := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:%d/proxy%s", namespace, service, grafanaProxyListenPort, upstreamRequest.URL.Path)
	if upstreamRequest.URL.RawQuery != "" {
		proxyPath += "?" + upstreamRequest.URL.RawQuery
	}
	headers := grafanaRequestHeaders(upstreamRequest.Header)
	resp, err := requester.DoWithGrafanaAuth(r.Context(), clusterID, r.Method, proxyPath, body, headers, auth)
	if err != nil || resp == nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.ProxyError, "Shared Grafana is unavailable")
		return
	}

	if isServiceProxyAuditMethod(r.Method) {
		recordAudit(r, h.queries, "monitoring.grafana.proxy", "cluster", clusterID, service, map[string]any{
			"namespace": namespace, "method": r.Method, "path": upstreamRequest.URL.Path,
		})
	}
	prefix := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:%d/proxy", namespace, service, grafanaProxyListenPort)
	writeGrafanaResponse(w, r, resp, prefix)
}

func clusterGrafanaProxyPath(clusterID string) string {
	return "/api/v1/clusters/" + clusterID + "/observability/grafana/"
}

func (h *MonitoringHandler) grafanaProxyAuth(r *http.Request, user *reqctx.User, email, role string, explore, admin bool, clusterIDs []string) (protocol.GrafanaProxyAuth, error) {

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		return protocol.GrafanaProxyAuth{}, err
	}
	token, _, err := h.grafanaTickets.Issue(userID, email, role, explore, admin, h.grafanaCookieTTL(r.Context()), clusterIDs)
	if err != nil {
		return protocol.GrafanaProxyAuth{}, err
	}
	return protocol.GrafanaProxyAuth{Ticket: token}, nil
}

func grafanaRequestHeaders(source http.Header) map[string]string {
	headers := map[string]string{"Accept-Encoding": "identity"}
	for _, key := range []string{"Accept", "Content-Type", "User-Agent"} {
		if value := source.Get(key); value != "" {
			headers[key] = value
		}
	}
	return headers
}
