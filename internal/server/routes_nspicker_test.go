package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// clusterReadBindings grants clusters:read cluster-wide — the persona that has
// always been able to open the Namespaces/Events pages via the coarse path.
func clusterReadBindings(clusterID uuid.UUID) []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceClusters), Verbs: []string{string(rbac.VerbRead)}}},
	}}
}

// TestRequireNamespacePickerListPermission verifies the fix for the Namespaces &
// Events list gate: a namespace-scoped project persona (granted workloads/pods,
// NOT clusters:read) must reach the handler when the flag is on — previously it
// got a hard 403 because the gate keyed off clusters:read. Flag-off behavior is
// preserved: only clusters:read admits.
func TestRequireNamespacePickerListPermission(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	run := func(flagOn bool, bindings []rbac.RoleBinding) int {
		mw := requireNamespacePickerListPermission(rbac.NewEngine(), routeSecurityRBACQuerier{bindings: bindings}, flagOn)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/namespaces/", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("cluster_id", clusterID.String())
		ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
		ctx = appmiddleware.SetAuthenticatedUserForTest(ctx, &appmiddleware.AuthenticatedUser{ID: uuid.New().String()})
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		mw(next).ServeHTTP(rec, req)
		return rec.Code
	}

	workloadsPersona := namespaceScopedListBindings(clusterID, "team-a", rbac.ResourceWorkloads)
	podsPersona := namespaceScopedListBindings(clusterID, "team-a", rbac.ResourcePods)
	secretsPersona := namespaceScopedListBindings(clusterID, "team-a", rbac.ResourceSecrets)
	clusterReader := clusterReadBindings(clusterID)

	tests := []struct {
		name     string
		flagOn   bool
		bindings []rbac.RoleBinding
		want     int
	}{
		{"workloads persona admitted when flag on", true, workloadsPersona, http.StatusOK},
		{"pods persona admitted when flag on", true, podsPersona, http.StatusOK},
		{"workloads persona 403 when flag off (behavior preserved)", false, workloadsPersona, http.StatusForbidden},
		{"clusters:read admitted when flag off", false, clusterReader, http.StatusOK},
		{"clusters:read admitted when flag on", true, clusterReader, http.StatusOK},
		{"unrelated resource still 403 when flag on", true, secretsPersona, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(tt.flagOn, tt.bindings); got != tt.want {
				t.Fatalf("status = %d, want %d", got, tt.want)
			}
		})
	}
}
