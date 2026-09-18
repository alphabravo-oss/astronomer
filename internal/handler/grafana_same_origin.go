package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/grafanaproxy"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const grafanaProxyCSP = "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'self'; img-src 'self' data: blob:; font-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; connect-src 'self' ws: wss:; worker-src 'self' blob:"

type grafanaAuthenticatedRequester interface {
	DoWithGrafanaAuth(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string, auth protocol.GrafanaProxyAuth) (*protocol.K8sResponsePayload, error)
}

// ProxyGrafana serves shared Grafana through Astronomer's authenticated origin.
// The browser never learns the in-cluster Service address and no second public
// hostname exists. Authorization is evaluated on every request, and scoped
// users have datasource queries rewritten to their allowed cluster IDs before
// the request enters the tunnel.
func (h *MonitoringHandler) ProxyGrafana(w http.ResponseWriter, r *http.Request) {
	if !h.authz.authorizeAnyScope(w, r, rbac.ResourceMonitoring, rbac.VerbRead) {
		return
	}
	requester, ok := h.requester.(grafanaAuthenticatedRequester)
	if !ok || h.queries == nil || h.grafanaTickets == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Grafana proxy is not configured")
		return
	}
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok || user == nil || user.ID == "" {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}

	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Shared Grafana is not configured")
		return
	}
	metadata := sharedStackMetadata(backend, "sharedGrafana")
	if !grafanaStackPresent(stringFromMap(metadata, "status")) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Shared Grafana is not installed")
		return
	}
	clusterID := strings.TrimSpace(stringFromMap(metadata, "managementClusterId"))
	namespace := defaultString(stringFromMap(metadata, "namespace"), "monitoring")
	release := defaultString(stringFromMap(metadata, "releaseName"), sharedGrafanaDefaultRelease)
	service := grafanaProxyServiceName(release)
	if _, err := uuid.Parse(clusterID); err != nil || !isSafeK8sName(namespace) || !isSafeK8sName(service) {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Shared Grafana target is invalid")
		return
	}

	upstreamRequest := r.Clone(r.Context())
	suffix := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	upstreamRequest.URL.Path = "/" + suffix
	upstreamRequest.URL.RawPath = ""
	email, role, explore, admin, clusterIDs := h.grafanaIdentity(r, user)
	if email == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Authenticated user has no email")
		return
	}
	upstreamRequest, status, err := grafanaproxy.PrepareRequest(upstreamRequest, grafanaproxy.Identity{
		Email: email, Role: role, Explore: explore, Admin: admin, ClusterIDs: clusterIDs,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Grafana query")
		return
	}
	if status != 0 {
		RespondRequestError(w, r, status, apierror.Forbidden, http.StatusText(status))
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, upstreamRequest.Body, serviceProxyMaxBodyBytes))
	if err != nil {
		RespondRequestError(w, r, http.StatusRequestEntityTooLarge, apierror.InvalidBody, "Grafana request exceeds maximum allowed size")
		return
	}
	auth, err := h.grafanaProxyAuth(r, user, email, role, explore, admin, clusterIDs)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TicketError, "Failed to authorize Grafana request")
		return
	}

	proxyPath := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:%d/proxy%s", namespace, service, grafanaProxyListenPort, upstreamRequest.URL.Path)
	if upstreamRequest.URL.RawQuery != "" {
		proxyPath += "?" + upstreamRequest.URL.RawQuery
	}
	headers := grafanaRequestHeaders(upstreamRequest.Header)
	resp, err := requester.DoWithGrafanaAuth(r.Context(), clusterID, r.Method, proxyPath, body, headers, auth)
	if err != nil || resp == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, "Shared Grafana is unavailable")
		return
	}

	for key, value := range resp.Headers {
		if strings.EqualFold(key, "Set-Cookie") {
			copyGrafanaCookie(w.Header(), value)
			continue
		}
		if serviceProxyResponseHeaderAllowed(key) {
			w.Header().Set(key, value)
		}
	}
	// The global API policy intentionally forbids framing and inline scripts.
	// Grafana needs both, so loosen them only on this authenticated endpoint.
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", grafanaProxyCSP)

	if isServiceProxyAuditMethod(r.Method) {
		recordAudit(r, h.queries, "monitoring.grafana.proxy", "cluster", clusterID, service, map[string]any{
			"namespace": namespace, "method": r.Method, "path": upstreamRequest.URL.Path,
		})
	}
	decoded, decodeErr := decodeResponseBody(resp)
	if decodeErr != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ProxyError, "Invalid Grafana response")
		return
	}
	statusCode := resp.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	w.WriteHeader(statusCode)
	_, _ = w.Write(decoded)
}

// ProxyClusterGrafana serves the Grafana bundled with one cluster's
// kube-prometheus-stack. The service remains ClusterIP-only; the browser sees
// only this same-origin URL, and the route's cluster-scoped monitoring:read
// middleware is the authorization boundary. Cluster Grafana is configured as
// an anonymous Viewer because the request already passed Astronomer RBAC and
// the service is never published directly.
func (h *MonitoringHandler) ProxyClusterGrafana(w http.ResponseWriter, r *http.Request) {
	clusterUUID, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}
	if h.requester == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Cluster Grafana proxy is not configured")
		return
	}
	cfg, err := h.queries.GetClusterMonitoringConfig(r.Context(), clusterUUID)
	if err != nil || cfg.Status == "not_configured" || cfg.Status == "uninstalled" {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster Grafana is not installed")
		return
	}
	service, err := h.findGrafanaServiceName(r.Context(), clusterUUID.String(), cfg.StackNamespace, cfg.PrometheusReleaseName)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster Grafana is not installed")
		return
	}

	suffix := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	proxyPath := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:80/proxy/", cfg.StackNamespace, service)
	if suffix != "" {
		proxyPath += suffix
	}
	if r.URL.RawQuery != "" {
		proxyPath += "?" + r.URL.RawQuery
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, serviceProxyMaxBodyBytes))
	if err != nil {
		RespondRequestError(w, r, http.StatusRequestEntityTooLarge, apierror.InvalidBody, "Grafana request exceeds maximum allowed size")
		return
	}
	resp, err := h.requester.Do(r.Context(), clusterUUID.String(), r.Method, proxyPath, body, grafanaRequestHeaders(r.Header))
	if err != nil || resp == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, "Cluster Grafana is unavailable")
		return
	}
	for key, value := range resp.Headers {
		if serviceProxyResponseHeaderAllowed(key) {
			w.Header().Set(key, value)
		}
	}
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", grafanaProxyCSP)
	if isServiceProxyAuditMethod(r.Method) {
		recordAudit(r, h.queries, "monitoring.cluster_grafana.proxy", "cluster", clusterUUID.String(), service, map[string]any{
			"namespace": cfg.StackNamespace, "method": r.Method, "path": "/" + suffix,
		})
	}
	decoded, decodeErr := decodeResponseBody(resp)
	if decodeErr != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ProxyError, "Invalid Grafana response")
		return
	}
	statusCode := resp.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	w.WriteHeader(statusCode)
	_, _ = w.Write(decoded)
}

func clusterGrafanaProxyPath(clusterID string) string {
	return "/api/v1/clusters/" + clusterID + "/observability/grafana/"
}

func (h *MonitoringHandler) grafanaProxyAuth(r *http.Request, user *reqctx.User, email, role string, explore, admin bool, clusterIDs []string) (protocol.GrafanaProxyAuth, error) {
	if cookie, err := r.Cookie(protocol.GrafanaProxyCookieName); err == nil && cookie.Value != "" {
		return protocol.GrafanaProxyAuth{Cookie: cookie.Value}, nil
	}
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
	headers := map[string]string{}
	for _, key := range []string{"Accept", "Accept-Encoding", "Content-Type", "User-Agent"} {
		if value := source.Get(key); value != "" {
			headers[key] = value
		}
	}
	return headers
}

func copyGrafanaCookie(headers http.Header, raw string) {
	cookie, err := http.ParseSetCookie(raw)
	if err != nil || cookie.Name != protocol.GrafanaProxyCookieName || cookie.Value == "" {
		return
	}
	cookie.Path = sharedGrafanaProxyPath
	cookie.HttpOnly = true
	cookie.SameSite = http.SameSiteStrictMode
	headers.Add("Set-Cookie", cookie.String())
}
