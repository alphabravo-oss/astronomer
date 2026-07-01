package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// podsAcrossNamespaces returns a stub requester serving pods in three
// namespaces for /api/v1/pods.
func podsAcrossNamespaces(t *testing.T) *stubK8sRequester {
	t.Helper()
	pods := []map[string]any{
		{"metadata": map[string]any{"name": "pod-a", "namespace": "team-a"}},
		{"metadata": map[string]any{"name": "pod-b", "namespace": "team-b"}},
		{"metadata": map[string]any{"name": "pod-sys", "namespace": "kube-system"}},
	}
	body, _ := json.Marshal(map[string]any{"items": pods})
	return &stubK8sRequester{respFn: func(_ stubReq) (*protocol.K8sResponsePayload, error) {
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString(body)}, nil
	}}
}

func doListPods(t *testing.T, h *WorkloadHandler, clusterID string) listEnvelope {
	t.Helper()
	rc := chi.NewRouteContext()
	rc.URLParams.Add("cluster_id", clusterID)
	req := httptest.NewRequest(http.MethodGet, "/clusters/"+clusterID+"/pods/", nil)
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: uuid.New().String(), Email: "u@test.com"})
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rc)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ListPods(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var env listEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env
}

func podNamespaces(env listEnvelope) map[string]bool {
	out := map[string]bool{}
	for _, item := range env.Data {
		if ns, ok := item["namespace"].(string); ok {
			out[ns] = true
		}
	}
	return out
}

// TestListPods_NamespaceScopedFiltering covers item 4 / regression (i)+(ii): a
// namespace-scoped reader's bare pod list is filtered to their authorized
// namespace only.
func TestListPods_NamespaceScopedFiltering(t *testing.T) {
	clusterID := uuid.New().String()
	engine := rbac.NewEngine()
	binding := []rbac.RoleBinding{{ClusterID: clusterID, Namespace: "team-a", RoleRules: []rbac.Rule{{Resource: "pods", Verbs: []string{"list"}}}}}

	h := NewWorkloadHandlerWithRequester(podsAcrossNamespaces(t))
	h.SetAuthorization(engine, stubWorkloadRBACQuerier{bindings: binding})
	h.SetNamespaceScopedRBAC(true)

	env := doListPods(t, h, clusterID)
	got := podNamespaces(env)
	if len(env.Data) != 1 || !got["team-a"] {
		t.Fatalf("expected only team-a pods, got namespaces %v (%d items)", got, len(env.Data))
	}
	if got["team-b"] || got["kube-system"] {
		t.Fatalf("scoped user leaked unauthorized namespaces: %v", got)
	}
}

// TestListPods_ClusterWideSeeEverything covers regression (iii): a cluster-wide
// reader (and a superuser) see all namespaces.
func TestListPods_ClusterWideSeeEverything(t *testing.T) {
	clusterID := uuid.New().String()
	engine := rbac.NewEngine()

	for _, tc := range []struct {
		name     string
		bindings []rbac.RoleBinding
	}{
		{"cluster-wide reader", []rbac.RoleBinding{{ClusterID: clusterID, RoleRules: []rbac.Rule{{Resource: "*", Verbs: []string{"read", "list"}}}}}},
		{"superuser", []rbac.RoleBinding{{IsSuperuser: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewWorkloadHandlerWithRequester(podsAcrossNamespaces(t))
			h.SetAuthorization(engine, stubWorkloadRBACQuerier{bindings: tc.bindings})
			h.SetNamespaceScopedRBAC(true)

			env := doListPods(t, h, clusterID)
			if len(env.Data) != 3 {
				t.Fatalf("cluster-wide reader should see all 3 pods, got %d", len(env.Data))
			}
		})
	}
}

// TestListPods_FlagOffNoFiltering covers regression (iv): with the flag off the
// list is byte-identical to today — no filtering even for a namespace-scoped
// binding (the gate, tested separately, would 403 such a bare list; the handler
// itself must not filter when disabled).
func TestListPods_FlagOffNoFiltering(t *testing.T) {
	clusterID := uuid.New().String()
	engine := rbac.NewEngine()
	binding := []rbac.RoleBinding{{ClusterID: clusterID, Namespace: "team-a", RoleRules: []rbac.Rule{{Resource: "pods", Verbs: []string{"list"}}}}}

	h := NewWorkloadHandlerWithRequester(podsAcrossNamespaces(t))
	h.SetAuthorization(engine, stubWorkloadRBACQuerier{bindings: binding})
	// SetNamespaceScopedRBAC NOT called => flag off (default).

	env := doListPods(t, h, clusterID)
	if len(env.Data) != 3 {
		t.Fatalf("flag off must return all pods unfiltered, got %d", len(env.Data))
	}
}
