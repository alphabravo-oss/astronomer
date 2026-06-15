package handler

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type serviceProxyTestRequester struct {
	path string
	resp *protocol.K8sResponsePayload
}

func (r *serviceProxyTestRequester) Do(_ context.Context, _, _, path string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	r.path = path
	if r.resp != nil {
		return r.resp, nil
	}
	return &protocol.K8sResponsePayload{StatusCode: http.StatusNoContent}, nil
}

type serviceProxyTestTools struct {
	tools []sqlc.ClusterTool
}

func (q serviceProxyTestTools) ListEnabledTools(context.Context) ([]sqlc.ClusterTool, error) {
	return q.tools, nil
}

type serviceProxyTestAuditWriter struct {
	rows []sqlc.CreateAuditLogV1Params
}

func (w *serviceProxyTestAuditWriter) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	w.rows = append(w.rows, arg)
	return nil
}

func TestServiceProxyAllowsEnabledToolService(t *testing.T) {
	requester := &serviceProxyTestRequester{}
	h := NewServiceProxyHandler(requester)
	h.SetToolQuerier(serviceProxyTestTools{tools: []sqlc.ClusterTool{{
		ServiceName: "grafana",
		ServicePort: pgtype.Int4{Int32: 3000, Valid: true},
	}}})
	router := serviceProxyTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/clusters/cluster-1/proxy/service/observability/grafana:3000/dashboards?orgId=1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	wantPath := "/api/v1/namespaces/observability/services/http:grafana:3000/proxy/dashboards?orgId=1"
	if requester.path != wantPath {
		t.Fatalf("path = %q, want %q", requester.path, wantPath)
	}
}

func TestServiceProxyAuditsMutatingRequests(t *testing.T) {
	requester := &serviceProxyTestRequester{}
	audit := &serviceProxyTestAuditWriter{}
	h := NewServiceProxyHandler(requester)
	h.SetToolQuerier(serviceProxyTestTools{tools: []sqlc.ClusterTool{{
		ServiceName: "grafana",
		ServicePort: pgtype.Int4{Int32: 3000, Valid: true},
	}}})
	h.SetAuditWriter(audit)
	router := serviceProxyTestRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/clusters/cluster-1/proxy/service/observability/grafana:3000/api/admin", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if len(audit.rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(audit.rows))
	}
	if audit.rows[0].Action != "cluster.service_proxy.forwarded" {
		t.Fatalf("audit action = %q", audit.rows[0].Action)
	}
	if audit.rows[0].ResourceID != "cluster-1" || audit.rows[0].ResourceName != "grafana" {
		t.Fatalf("audit resource = %s/%s", audit.rows[0].ResourceID, audit.rows[0].ResourceName)
	}
}

func TestServiceProxySanitizesResponseHeaders(t *testing.T) {
	requester := &serviceProxyTestRequester{resp: &protocol.K8sResponsePayload{
		StatusCode: http.StatusOK,
		Headers: map[string]string{
			"Cache-Control":      "no-store",
			"Clear-Site-Data":    `"cookies"`,
			"Connection":         "upgrade",
			"Content-Length":     "999",
			"Content-Type":       "application/json",
			"Proxy-Authenticate": "Basic",
			"Set-Cookie":         "grafana_session=abc; Path=/",
			"Transfer-Encoding":  "chunked",
			"WWW-Authenticate":   "Bearer",
			"X-Frame-Options":    "SAMEORIGIN",
		},
		Body: base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`)),
	}}
	h := NewServiceProxyHandler(requester)
	h.SetToolQuerier(serviceProxyTestTools{tools: []sqlc.ClusterTool{{
		ServiceName: "grafana",
		ServicePort: pgtype.Int4{Int32: 3000, Valid: true},
	}}})
	router := serviceProxyTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/clusters/cluster-1/proxy/service/observability/grafana:3000/api/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	for _, name := range []string{
		"Clear-Site-Data",
		"Connection",
		"Content-Length",
		"Proxy-Authenticate",
		"Set-Cookie",
		"Transfer-Encoding",
		"WWW-Authenticate",
	} {
		if got := rec.Header().Get(name); got != "" {
			t.Fatalf("%s forwarded as %q", name, got)
		}
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("X-Frame-Options = %q, want SAMEORIGIN", got)
	}
	if got := rec.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q", got)
	}
}

func TestServiceProxyAllowsEnabledSubService(t *testing.T) {
	requester := &serviceProxyTestRequester{}
	h := NewServiceProxyHandler(requester)
	h.SetToolQuerier(serviceProxyTestTools{tools: []sqlc.ClusterTool{{
		SubServices: []byte(`[{"service":"prometheus-operated","port":9090}]`),
	}}})
	router := serviceProxyTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/clusters/cluster-1/proxy/service/monitoring/prometheus-operated:9090/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
}

func TestServiceProxyBlocksToolWhenMetadataDisallowsProxy(t *testing.T) {
	requester := &serviceProxyTestRequester{}
	h := NewServiceProxyHandler(requester)
	h.SetToolQuerier(serviceProxyTestTools{tools: []sqlc.ClusterTool{{
		Presets:     []byte(`{"service_proxy_allowed":false}`),
		ServiceName: "grafana",
		ServicePort: pgtype.Int4{Int32: 3000, Valid: true},
	}}})
	router := serviceProxyTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/clusters/cluster-1/proxy/service/observability/grafana:3000/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if requester.path != "" {
		t.Fatalf("requester was called for denied target: %q", requester.path)
	}
}

func TestServiceProxyBlocksSubServiceWhenMetadataDisallowsProxy(t *testing.T) {
	requester := &serviceProxyTestRequester{}
	h := NewServiceProxyHandler(requester)
	h.SetToolQuerier(serviceProxyTestTools{tools: []sqlc.ClusterTool{{
		SubServices: []byte(`[{"service":"prometheus-operated","port":9090,"service_proxy_allowed":false},{"service":"grafana","port":3000}]`),
	}}})
	router := serviceProxyTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/clusters/cluster-1/proxy/service/monitoring/prometheus-operated:9090/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if requester.path != "" {
		t.Fatalf("requester was called for denied target: %q", requester.path)
	}
}

func TestServiceProxyBlocksUnknownService(t *testing.T) {
	requester := &serviceProxyTestRequester{}
	h := NewServiceProxyHandler(requester)
	h.SetToolQuerier(serviceProxyTestTools{tools: []sqlc.ClusterTool{{
		ServiceName: "grafana",
		ServicePort: pgtype.Int4{Int32: 3000, Valid: true},
	}}})
	router := serviceProxyTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/clusters/cluster-1/proxy/service/observability/prometheus:9090/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if requester.path != "" {
		t.Fatalf("requester was called for denied target: %q", requester.path)
	}
}

func TestServiceProxyBlocksSensitiveNamespace(t *testing.T) {
	requester := &serviceProxyTestRequester{}
	h := NewServiceProxyHandler(requester)
	h.SetToolQuerier(serviceProxyTestTools{tools: []sqlc.ClusterTool{{
		ServiceName: "kube-dns",
		ServicePort: pgtype.Int4{Int32: 53, Valid: true},
	}}})
	router := serviceProxyTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/clusters/cluster-1/proxy/service/kube-system/kube-dns:53/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if requester.path != "" {
		t.Fatalf("requester was called for denied target: %q", requester.path)
	}
}

func serviceProxyTestRouter(h *ServiceProxyHandler) http.Handler {
	r := chi.NewRouter()
	r.Handle("/clusters/{cluster_id}/proxy/service/{namespace}/{service_port}/", h)
	r.Handle("/clusters/{cluster_id}/proxy/service/{namespace}/{service_port}/*", h)
	return r
}
