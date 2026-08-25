package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type k8sProxyBoundaryAuditWriter struct {
	mu       sync.Mutex
	attempts int
	failAt   int
	rows     []sqlc.CreateAuditLogV1Params
}

func (w *k8sProxyBoundaryAuditWriter) CreateAuditLogV1(_ context.Context, row sqlc.CreateAuditLogV1Params) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.attempts++
	if w.failAt == w.attempts {
		return errors.New("audit persistence unavailable")
	}
	w.rows = append(w.rows, row)
	return nil
}

func (w *k8sProxyBoundaryAuditWriter) snapshot() (int, []sqlc.CreateAuditLogV1Params) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.attempts, append([]sqlc.CreateAuditLogV1Params(nil), w.rows...)
}

func k8sProxyBoundaryRouter(writer any, next http.HandlerFunc) http.Handler {
	router := chi.NewRouter()
	router.With(auditK8sProxyMutations(writer)).HandleFunc("/clusters/{cluster_id}/k8s/*", next)
	return router
}

func TestK8sProxyAuditMethodClassificationCoversRouteMethods(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run("read_"+method, func(t *testing.T) {
			writer := &k8sProxyBoundaryAuditWriter{}
			called := false
			router := k8sProxyBoundaryRouter(writer, func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(method, "/clusters/c1/k8s/api/v1/pods", nil))
			attempts, _ := writer.snapshot()
			if !called || attempts != 0 {
				t.Fatalf("called=%v audit attempts=%d, want true/0", called, attempts)
			}
		})
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodConnect, http.MethodTrace} {
		t.Run("mutation_"+method, func(t *testing.T) {
			writer := &k8sProxyBoundaryAuditWriter{}
			router := k8sProxyBoundaryRouter(writer, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(method, "/clusters/c1/k8s/apis/apps/v1/namespaces/team-a/deployments/web", nil))
			attempts, rows := writer.snapshot()
			if rec.Code != http.StatusAccepted || attempts != 2 || len(rows) != 2 {
				t.Fatalf("status=%d attempts=%d rows=%d, want 202/2/2", rec.Code, attempts, len(rows))
			}
			if rows[0].Action != "cluster.k8s_proxy.intent" || rows[1].Action != "cluster.k8s_proxy.outcome" {
				t.Fatalf("actions = %q, %q", rows[0].Action, rows[1].Action)
			}
		})
	}
	if !isMutatingK8sProxyMethod("CUSTOM") {
		t.Fatal("unknown methods must fail closed as mutating")
	}
}

func TestK8sProxyAuditIntentFailureStopsRemoteEffect(t *testing.T) {
	writer := &k8sProxyBoundaryAuditWriter{failAt: 1}
	called := false
	router := k8sProxyBoundaryRouter(writer, func(http.ResponseWriter, *http.Request) { called = true })
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/clusters/c1/k8s/api/v1/namespaces/default/pods/web", nil))

	attempts, rows := writer.snapshot()
	if called || attempts != 1 || len(rows) != 0 {
		t.Fatalf("called=%v attempts=%d rows=%d, want false/1/0", called, attempts, len(rows))
	}
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "audit_unavailable") {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
}

func TestK8sProxyAuditOutcomeFailurePreservesSynchronousResponse(t *testing.T) {
	writer := &k8sProxyBoundaryAuditWriter{failAt: 2}
	router := k8sProxyBoundaryRouter(writer, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Upstream", "preserved")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("member-cluster-response"))
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/clusters/c1/k8s/api/v1/namespaces/default/configmaps/settings", strings.NewReader(`{"data":{"x":"y"}}`)))

	attempts, rows := writer.snapshot()
	if attempts != 2 || len(rows) != 1 || rows[0].Action != "cluster.k8s_proxy.intent" {
		t.Fatalf("attempts=%d rows=%#v, want durable intent and failed outcome", attempts, rows)
	}
	var intentDetail map[string]any
	if err := json.Unmarshal(rows[0].Detail, &intentDetail); err != nil {
		t.Fatal(err)
	}
	if intentDetail["terminal_state"] != "unknown_until_outcome" || intentDetail["reconcile_if_outcome_missing"] != true {
		t.Fatalf("durable intent does not expose unknown/repair semantics: %#v", intentDetail)
	}
	if rec.Code != http.StatusTeapot || rec.Header().Get("X-Upstream") != "preserved" || rec.Body.String() != "member-cluster-response" {
		t.Fatalf("response changed after outcome failure: %d headers=%v body=%q", rec.Code, rec.Header(), rec.Body.String())
	}
}

func TestK8sProxyAuditEvidenceExcludesBodyHeadersAndQuery(t *testing.T) {
	const sensitive = "TOP-SECRET-SENTINEL"
	writer := &k8sProxyBoundaryAuditWriter{}
	router := k8sProxyBoundaryRouter(writer, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodPost,
		"/clusters/c1/k8s/api/v1/namespaces/default/secrets/db-password?token="+sensitive,
		strings.NewReader("apiVersion: v1\nkind: Secret\nstringData:\n  password: "+sensitive))
	req.Header.Set("Authorization", "Bearer "+sensitive)
	req.Header.Set("X-Secret", sensitive)
	req.Header.Set("User-Agent", sensitive)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	_, rows := writer.snapshot()
	if len(rows) != 2 {
		t.Fatalf("rows=%d, want 2", len(rows))
	}
	for _, row := range rows {
		encoded, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), sensitive) || strings.Contains(row.Path, "?") {
			t.Fatalf("audit evidence leaked body/header/query: %s", encoded)
		}
		var detail map[string]any
		if err := json.Unmarshal(row.Detail, &detail); err != nil {
			t.Fatal(err)
		}
		if detail["resource"] != "secrets" || detail["name"] != "db-password" {
			t.Fatalf("sanitized object coordinates missing: %#v", detail)
		}
	}
}

func TestK8sProxyMandatoryAuditIsWiredAtProductionRouteEntry(t *testing.T) {
	routesSource, err := os.ReadFile("routes_long_lived.go")
	if err != nil {
		t.Fatal(err)
	}
	routesText := string(routesSource)
	for _, required := range []string{
		"auditK8sProxyMutations(deps.AuditWriter)",
		`HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", deps.Proxy.HandleK8sProxy)`,
	} {
		if !strings.Contains(routesText, required) {
			t.Fatalf("production k8s proxy route missing %q", required)
		}
	}
	serverSource, err := os.ReadFile("app_router_dependencies_core.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(serverSource), "AuditWriter:         queries") {
		t.Fatal("production router does not wire the durable SQL audit writer")
	}
}
