package handler

import (
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/grafanaproxy"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Kubernetes' service proxy rewrites HTML links and redirects to its own API
// prefix. Remove that exact internal prefix at the public Grafana boundary.
func writeGrafanaResponse(w http.ResponseWriter, r *http.Request, resp *protocol.K8sResponsePayload, prefix string) {
	body, err := decodeResponseBody(resp)
	if err != nil {
		RespondRequestError(w, r, 502, apierror.ProxyError, "Invalid Grafana response")
		return
	}
	for key, value := range resp.Headers {
		if serviceProxyResponseHeaderAllowed(key) {
			w.Header().Set(key, value)
		}
	}
	if strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		body = grafanaproxy.PrepareHTML(body, prefix)
	}
	if location := w.Header().Get("Location"); strings.HasPrefix(location, prefix+"/") {
		w.Header().Set("Location", strings.TrimPrefix(location, prefix))
	}
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", grafanaProxyCSP)
	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
