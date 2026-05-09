package handler

import (
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

type ServiceProxyHandler struct {
	requester K8sRequester
}

func NewServiceProxyHandler(requester K8sRequester) *ServiceProxyHandler {
	return &ServiceProxyHandler{requester: requester}
}

func (h *ServiceProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	namespace := chi.URLParam(r, "namespace")
	servicePort := chi.URLParam(r, "service_port")
	pathSuffix := chi.URLParam(r, "*")

	if h.requester == nil {
		RespondError(w, http.StatusServiceUnavailable, "proxy_error", "service proxy not configured")
		return
	}

	target := servicePort
	if !strings.Contains(target, ":") {
		target = target + ":80"
	}
	proxyPath := "/api/v1/namespaces/" + namespace + "/services/http:" + target + "/proxy"
	if pathSuffix != "" {
		proxyPath += "/" + pathSuffix
	} else {
		proxyPath += "/"
	}
	if r.URL.RawQuery != "" {
		proxyPath += "?" + r.URL.RawQuery
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Failed to read request body")
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
		RespondError(w, http.StatusServiceUnavailable, "proxy_error", err.Error())
		return
	}

	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	body, _ = decodeResponseBody(resp)
	if resp.StatusCode == 0 {
		resp.StatusCode = http.StatusOK
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}
