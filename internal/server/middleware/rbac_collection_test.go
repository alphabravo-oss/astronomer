package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

func runCollectionGate(t *testing.T, bindings []rbac.RoleBinding, query string) int {
	t.Helper()
	engine := rbac.NewEngine()
	querier := &mockRBACQuerier{bindings: bindings}
	mw := RequireCollectionPermission(engine, querier, rbac.ResourceClusters, rbac.VerbList)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	req := httptest.NewRequest(http.MethodGet, "/clusters/"+query, nil)
	ctx := SetAuthenticatedUserForTest(req.Context(), &AuthenticatedUser{ID: uuid.New().String(), Email: "u@test.com"})
	rr := httptest.NewRecorder()
	// No URL params: a collection route binds neither {id} nor {cluster_id}.
	handler.ServeHTTP(rr, setupChiRequest(req.WithContext(ctx), nil))
	return rr.Code
}

// TestRequireCollectionPermission_GateBehaviour pins the relaxed collection
// gate. The old gate ran at uuid.Nil scope, where bindingApplies only ever
// matches a GLOBAL binding, so every cluster-/project-scoped caller was 403'd
// off the fleet landing page.
func TestRequireCollectionPermission_GateBehaviour(t *testing.T) {
	clusterA := uuid.New().String()
	projectA := uuid.New().String()
	listRules := []rbac.Rule{{Resource: "*", Verbs: []string{"read", "list"}}}

	cases := []struct {
		name     string
		bindings []rbac.RoleBinding
		query    string
		want     int
	}{
		{
			name:     "global reader allowed (unchanged)",
			bindings: []rbac.RoleBinding{{RoleRules: listRules}},
			want:     http.StatusOK,
		},
		{
			name:     "superuser allowed (unchanged)",
			bindings: []rbac.RoleBinding{{IsSuperuser: true}},
			want:     http.StatusOK,
		},
		{
			name:     "cluster-scoped caller allowed (was 403)",
			bindings: []rbac.RoleBinding{{Scope: "cluster", ClusterID: clusterA, RoleRules: listRules}},
			want:     http.StatusOK,
		},
		{
			name:     "project-scoped caller allowed (was 403)",
			bindings: []rbac.RoleBinding{{Scope: "project", ProjectID: projectA, RoleRules: listRules}},
			want:     http.StatusOK,
		},
		{
			name:     "no clusters:list grant anywhere still denied",
			bindings: []rbac.RoleBinding{{Scope: "cluster", ClusterID: clusterA, RoleRules: []rbac.Rule{{Resource: "monitoring", Verbs: []string{"read"}}}}},
			want:     http.StatusForbidden,
		},
		{
			name:     "no bindings at all still denied",
			bindings: nil,
			want:     http.StatusForbidden,
		},
		{
			// A pinned ?namespace= is judged by the plain scope check only, same
			// as RequireListPermission, so it cannot be used to widen anything.
			name:     "pinned namespace does not get the scoped fallback",
			bindings: []rbac.RoleBinding{{Scope: "cluster", ClusterID: clusterA, RoleRules: listRules}},
			query:    "?namespace=team-a",
			want:     http.StatusForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runCollectionGate(t, tc.bindings, tc.query); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}
