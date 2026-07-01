package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// serviceProxyMaxBodyBytes caps how large a request body the service proxy will
// buffer into the control-plane process before forwarding it through the
// tunnel. Without a cap an authenticated user could POST a multi-gigabyte body
// to an allowlisted target and exhaust the process's memory (io.ReadAll has no
// bound). 10 MiB comfortably covers legitimate proxied API calls (Prometheus /
// Grafana / dashboard requests) while failing closed on abuse.
const serviceProxyMaxBodyBytes = 10 << 20

type ServiceProxyToolQuerier interface {
	ListEnabledTools(ctx context.Context) ([]sqlc.ClusterTool, error)
}

type ServiceProxyHandler struct {
	requester K8sRequester
	tools     ServiceProxyToolQuerier
	audit     any
}

func NewServiceProxyHandler(requester K8sRequester) *ServiceProxyHandler {
	return &ServiceProxyHandler{requester: requester}
}

func (h *ServiceProxyHandler) SetToolQuerier(tools ServiceProxyToolQuerier) {
	if h == nil {
		return
	}
	h.tools = tools
}

func (h *ServiceProxyHandler) SetAuditWriter(audit any) {
	if h == nil {
		return
	}
	h.audit = audit
}

func (h *ServiceProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	namespace := chi.URLParam(r, "namespace")
	servicePort := chi.URLParam(r, "service_port")
	pathSuffix := chi.URLParam(r, "*")

	if h.requester == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, "service proxy not configured")
		return
	}

	target, err := parseServiceProxyTarget(namespace, servicePort)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidServiceProxyTarget, err.Error())
		return
	}
	if err := h.authorizeTarget(r.Context(), target); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.ServiceProxyDenied, err.Error())
		return
	}
	if isServiceProxyAuditMethod(r.Method) {
		recordAudit(r, h.audit, "cluster.service_proxy.forwarded", "cluster", clusterID, target.serviceName, map[string]any{
			"namespace":   target.namespace,
			"service":     target.serviceName,
			"port":        target.port,
			"path_suffix": pathSuffix,
			"method":      r.Method,
		})
	}

	proxyPath := "/api/v1/namespaces/" + target.namespace + "/services/http:" + target.serviceName + ":" + target.port + "/proxy"
	if pathSuffix != "" {
		proxyPath += "/" + pathSuffix
	} else {
		proxyPath += "/"
	}
	if r.URL.RawQuery != "" {
		proxyPath += "?" + r.URL.RawQuery
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, serviceProxyMaxBodyBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			RespondRequestError(w, r, http.StatusRequestEntityTooLarge, apierror.InvalidBody, "request body exceeds maximum allowed size")
			return
		}
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Failed to read request body")
		return
	}
	headers := requestHeaders(r.Header.Get("Content-Type"))
	if accept := r.Header.Get("Accept"); accept != "" {
		headers["Accept"] = accept
	}

	resp, err := h.requester.Do(r.Context(), clusterID, r.Method, proxyPath, body, headers)
	if err != nil || ensureSuccess(resp) != nil {
		if err == nil {
			err = ensureSuccess(resp)
		}
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}

	for k, v := range resp.Headers {
		if serviceProxyResponseHeaderAllowed(k) {
			w.Header().Set(k, v)
		}
	}
	body, _ = decodeResponseBody(resp)
	if resp.StatusCode == 0 {
		resp.StatusCode = http.StatusOK
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

type serviceProxyTarget struct {
	namespace   string
	serviceName string
	port        string
}

func parseServiceProxyTarget(namespace, servicePort string) (serviceProxyTarget, error) {
	namespace = strings.TrimSpace(namespace)
	servicePort = strings.TrimSpace(servicePort)
	if !isSafeK8sName(namespace) {
		return serviceProxyTarget{}, httpError("invalid Kubernetes namespace")
	}
	if isSensitiveServiceProxyNamespace(namespace) {
		return serviceProxyTarget{}, httpError("service proxy is not allowed for this namespace")
	}
	serviceName := servicePort
	port := "80"
	if left, right, ok := strings.Cut(servicePort, ":"); ok {
		serviceName = left
		port = right
	}
	if !isSafeK8sName(serviceName) {
		return serviceProxyTarget{}, httpError("invalid Kubernetes service name")
	}
	if !isValidServiceProxyPort(port) {
		return serviceProxyTarget{}, httpError("invalid Kubernetes service port")
	}
	return serviceProxyTarget{namespace: namespace, serviceName: serviceName, port: port}, nil
}

func (h *ServiceProxyHandler) authorizeTarget(ctx context.Context, target serviceProxyTarget) error {
	if h == nil || h.tools == nil {
		return httpError("service proxy allowlist is not configured")
	}
	tools, err := h.tools.ListEnabledTools(ctx)
	if err != nil {
		return httpError("failed to load service proxy allowlist")
	}
	for _, tool := range tools {
		if serviceProxyToolAllowsTarget(tool, target) {
			return nil
		}
	}
	return httpError("service proxy target is not enabled")
}

func serviceProxyToolAllowsTarget(tool sqlc.ClusterTool, target serviceProxyTarget) bool {
	if !serviceProxyAllowedByPresets(tool.Presets) {
		return false
	}
	if tool.ServiceName != "" && tool.ServicePort.Valid {
		if tool.ServiceName == target.serviceName && strconv.Itoa(int(tool.ServicePort.Int32)) == target.port {
			return true
		}
	}
	if len(tool.SubServices) == 0 {
		return false
	}
	var subs []struct {
		Service             string `json:"service"`
		Port                int32  `json:"port"`
		ServiceProxyAllowed *bool  `json:"service_proxy_allowed"`
	}
	if err := json.Unmarshal(tool.SubServices, &subs); err != nil {
		return false
	}
	for _, sub := range subs {
		if sub.ServiceProxyAllowed != nil && !*sub.ServiceProxyAllowed {
			continue
		}
		if sub.Service == target.serviceName && strconv.Itoa(int(sub.Port)) == target.port {
			return true
		}
	}
	return false
}

func serviceProxyAllowedByPresets(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var presets map[string]any
	if err := json.Unmarshal(raw, &presets); err != nil {
		return true
	}
	v, ok := presets["service_proxy_allowed"]
	if !ok {
		return true
	}
	allowed, ok := v.(bool)
	return ok && allowed
}

func isSensitiveServiceProxyNamespace(namespace string) bool {
	switch namespace {
	case "kube-system", "kube-public", "kube-node-lease":
		return true
	default:
		return false
	}
}

func isSafeK8sName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for _, r := range name {
		if r == '-' || ('0' <= r && r <= '9') || ('a' <= r && r <= 'z') {
			continue
		}
		return false
	}
	return true
}

func isValidServiceProxyPort(port string) bool {
	if port == "" || len(port) > 5 {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

func isServiceProxyAuditMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func serviceProxyResponseHeaderAllowed(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch lower {
	case "":
		return false
	case "authorization", "clear-site-data", "content-length", "cookie", "proxy-authenticate", "proxy-authorization", "set-cookie", "set-cookie2", "www-authenticate":
		return false
	case "connection", "keep-alive", "te", "trailer", "trailers", "transfer-encoding", "upgrade":
		return false
	default:
		return true
	}
}

type httpError string

func (e httpError) Error() string { return string(e) }
