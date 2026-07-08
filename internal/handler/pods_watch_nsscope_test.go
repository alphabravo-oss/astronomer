package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// nsWatchFrames is a fixed set of pod-watch frames spanning two namespaces plus
// one object-less frame, used to prove per-frame namespace confinement.
func nsWatchFrames() []PodWatchEvent {
	return []PodWatchEvent{
		{Type: "ADDED", Object: json.RawMessage(`{"metadata":{"name":"a-0","namespace":"team-a"}}`)},
		{Type: "ADDED", Object: json.RawMessage(`{"metadata":{"name":"b-0","namespace":"team-b"}}`)},
		{Type: "MODIFIED", Object: json.RawMessage(`{"metadata":{"name":"a-0","namespace":"team-a"}}`)},
		// An object-less frame (e.g. a bare ERROR) has no namespace to prove
		// ownership of and must be dropped for a restricted caller.
		{Type: "ERROR", Object: nil},
	}
}

func doWatchPods(t *testing.T, h *WorkloadHandler, clusterID string, bindings []rbac.RoleBinding) string {
	t.Helper()
	rc := chi.NewRouteContext()
	rc.URLParams.Add("cluster_id", clusterID)
	req := httptest.NewRequest("GET", "/api/v1/clusters/"+clusterID+"/pods/watch/", nil)
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: uuid.New().String(), Email: "u@test.com"})
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rc)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.WatchPods(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestWatchPods_NamespaceScopedFiltering: a namespace-confined tenant admitted
// by the LIST gate receives frames only for their owned namespace; foreign and
// object-less frames are dropped (fail-closed allow-list).
func TestWatchPods_NamespaceScopedFiltering(t *testing.T) {
	clusterID := uuid.New().String()
	binding := []rbac.RoleBinding{{ClusterID: clusterID, Namespace: "team-a", RoleRules: []rbac.Rule{{Resource: "pods", Verbs: []string{"read"}}}}}

	h := NewWorkloadHandler()
	h.SetPodWatcher(&fakePodWatcher{events: nsWatchFrames()})
	h.SetAuthorization(rbac.NewEngine(), stubWorkloadRBACQuerier{bindings: binding})
	h.SetNamespaceScopedRBAC(true)

	body := doWatchPods(t, h, clusterID, binding)
	if !strings.Contains(body, `"namespace":"team-a"`) {
		t.Errorf("scoped user should receive owned team-a frames; body:\n%s", body)
	}
	if strings.Contains(body, `"namespace":"team-b"`) {
		t.Errorf("scoped user leaked foreign team-b frame; body:\n%s", body)
	}
	// The object-less ERROR frame carries no namespace and must not forward.
	if strings.Contains(body, "event: ERROR") {
		t.Errorf("scoped user should not receive object-less ERROR frame; body:\n%s", body)
	}
}

// TestWatchPods_ClusterWideUnaffected: a cluster-wide reader and a superuser see
// every namespace's frames unfiltered — no regression from the F7 gate/filter.
func TestWatchPods_ClusterWideUnaffected(t *testing.T) {
	clusterID := uuid.New().String()
	for _, tc := range []struct {
		name     string
		bindings []rbac.RoleBinding
	}{
		{"cluster-wide reader", []rbac.RoleBinding{{ClusterID: clusterID, RoleRules: []rbac.Rule{{Resource: "*", Verbs: []string{"read"}}}}}},
		{"superuser", []rbac.RoleBinding{{IsSuperuser: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewWorkloadHandler()
			h.SetPodWatcher(&fakePodWatcher{events: nsWatchFrames()})
			h.SetAuthorization(rbac.NewEngine(), stubWorkloadRBACQuerier{bindings: tc.bindings})
			h.SetNamespaceScopedRBAC(true)

			body := doWatchPods(t, h, clusterID, tc.bindings)
			if !strings.Contains(body, `"namespace":"team-a"`) || !strings.Contains(body, `"namespace":"team-b"`) {
				t.Errorf("cluster-wide caller should see all namespaces; body:\n%s", body)
			}
		})
	}
}

// TestWatchPods_FlagOffNoFiltering: with the flag off the handler forwards every
// frame unchanged, even for a namespace-scoped binding (the route gate — tested
// in the server package — governs admission; the handler must not filter).
func TestWatchPods_FlagOffNoFiltering(t *testing.T) {
	clusterID := uuid.New().String()
	binding := []rbac.RoleBinding{{ClusterID: clusterID, Namespace: "team-a", RoleRules: []rbac.Rule{{Resource: "pods", Verbs: []string{"read"}}}}}

	h := NewWorkloadHandler()
	h.SetPodWatcher(&fakePodWatcher{events: nsWatchFrames()})
	h.SetAuthorization(rbac.NewEngine(), stubWorkloadRBACQuerier{bindings: binding})
	// namespace_scoped_rbac_enabled OFF (default).

	body := doWatchPods(t, h, clusterID, binding)
	if !strings.Contains(body, `"namespace":"team-a"`) || !strings.Contains(body, `"namespace":"team-b"`) {
		t.Errorf("flag-off handler must forward all frames unchanged; body:\n%s", body)
	}
}
