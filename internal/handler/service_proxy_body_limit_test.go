package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// TestServiceProxyRejectsOversizeBody proves the service proxy caps the request
// body it buffers. Before the fix io.ReadAll(r.Body) buffered the entire body
// (multi-gigabyte POSTs would exhaust the control-plane process memory); now a
// body over serviceProxyMaxBodyBytes is rejected with 413 and never reaches the
// tunnel requester.
func TestServiceProxyRejectsOversizeBody(t *testing.T) {
	requester := &serviceProxyTestRequester{}
	h := NewServiceProxyHandler(requester)
	h.SetToolQuerier(serviceProxyTestTools{tools: []sqlc.ClusterTool{{
		ServiceName: "grafana",
		ServicePort: pgtype.Int4{Int32: 3000, Valid: true},
	}}})
	router := serviceProxyTestRouter(h)

	oversize := strings.Repeat("a", serviceProxyMaxBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/clusters/cluster-1/proxy/service/observability/grafana:3000/api/admin", strings.NewReader(oversize))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
	if requester.path != "" {
		t.Fatalf("oversize body was forwarded to the requester: %q", requester.path)
	}
}

// TestServiceProxyAllowsBodyUnderCap proves a legitimate mutating request whose
// body is within the cap still forwards successfully — the size guard must not
// regress the happy path.
func TestServiceProxyAllowsBodyUnderCap(t *testing.T) {
	requester := &serviceProxyTestRequester{}
	h := NewServiceProxyHandler(requester)
	h.SetToolQuerier(serviceProxyTestTools{tools: []sqlc.ClusterTool{{
		ServiceName: "grafana",
		ServicePort: pgtype.Int4{Int32: 3000, Valid: true},
	}}})
	router := serviceProxyTestRouter(h)

	body := strings.Repeat("a", 1024)
	req := httptest.NewRequest(http.MethodPost, "/clusters/cluster-1/proxy/service/observability/grafana:3000/api/admin", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	wantPath := "/api/v1/namespaces/observability/services/http:grafana:3000/proxy/api/admin"
	if requester.path != wantPath {
		t.Fatalf("path = %q, want %q", requester.path, wantPath)
	}
}
