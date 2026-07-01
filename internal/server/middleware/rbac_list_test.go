package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// projectNamespacesForTest stubs ListProjectNamespaces results for the shared
// fakeUserBindingsQuerier (declared in rbac_cache_test.go). Keyed by project
// UUID string. Only read when namespace scoping is enabled; the cache tests keep
// it off, so this global never interferes with them.
var projectNamespacesForTest = map[string][]sqlc.ProjectNamespace{}

// ListProjectNamespaces satisfies the userBindingsQuerier interface for the
// shared fake. Methods on a type may live in any file of the package.
func (f *fakeUserBindingsQuerier) ListProjectNamespaces(_ context.Context, projectID uuid.UUID) ([]sqlc.ProjectNamespace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return projectNamespacesForTest[projectID.String()], nil
}

func nsListReq(t *testing.T, clusterID, query string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/clusters/"+clusterID+"/pods/"+query, nil)
	ctx := SetAuthenticatedUserForTest(req.Context(), &AuthenticatedUser{ID: uuid.New().String(), Email: "u@test.com"})
	req = req.WithContext(ctx)
	return setupChiRequest(req, map[string]string{"cluster_id": clusterID})
}

func runListGate(t *testing.T, bindings []rbac.RoleBinding, flag bool, clusterID, query string) int {
	t.Helper()
	engine := rbac.NewEngine()
	querier := &mockRBACQuerier{bindings: bindings}
	mw := RequireListPermission(engine, querier, rbac.ResourcePods, rbac.VerbList, flag)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, nsListReq(t, clusterID, query))
	return rr.Code
}

func TestRequireListPermission_GateBehaviour(t *testing.T) {
	clusterA := uuid.New().String()
	projectID := uuid.New().String()

	nsBinding := []rbac.RoleBinding{{ClusterID: clusterA, Namespace: "team-a", RoleRules: []rbac.Rule{{Resource: "pods", Verbs: []string{"list"}}}}}
	clusterWide := []rbac.RoleBinding{{ClusterID: clusterA, RoleRules: []rbac.Rule{{Resource: "*", Verbs: []string{"read", "list"}}}}}
	superuser := []rbac.RoleBinding{{IsSuperuser: true}}
	projectBinding := []rbac.RoleBinding{{Scope: "project", ProjectID: projectID, RoleRules: []rbac.Rule{{Resource: "*", Verbs: []string{"read", "list"}}}}}

	cases := []struct {
		name     string
		bindings []rbac.RoleBinding
		flag     bool
		query    string
		want     int
	}{
		// (ii) namespace-scoped user's bare list is allowed through to the handler.
		{"ns-scoped bare list allowed (flag on)", nsBinding, true, "", http.StatusOK},
		// (iii) cluster-wide reader and superuser see everything.
		{"cluster-wide reader allowed", clusterWide, true, "", http.StatusOK},
		{"superuser allowed", superuser, true, "", http.StatusOK},
		// (v) crafted ?namespace= for an unauthorized namespace is denied at the gate.
		{"crafted unauthorized namespace denied", nsBinding, true, "?namespace=team-x", http.StatusForbidden},
		// authorized ?namespace= still allowed.
		{"authorized namespace allowed", nsBinding, true, "?namespace=team-a", http.StatusOK},
		// (iv) flag OFF => byte-identical to RequirePermission: namespace binding
		// fails closed on a bare list, project binding 403s on a cluster route.
		{"ns-scoped bare list denied (flag off)", nsBinding, false, "", http.StatusForbidden},
		{"project binding denied on cluster route (flag off)", projectBinding, false, "", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runListGate(t, tc.bindings, tc.flag, clusterA, tc.query); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestGetUserBindings_ProjectExpansion covers item 1: with the flag on, a project
// binding is expanded into synthetic namespace-scoped cluster bindings, and the
// pure engine then grants a cluster read scoped to the project's namespaces.
func TestGetUserBindings_ProjectExpansion(t *testing.T) {
	userID := uuid.New()
	projectID := uuid.New()
	clusterID := uuid.New()

	fake := newFakeUserBindingsQuerier()
	fake.setRows(userID.String(), []sqlc.ListUserBindingsWithRolesRow{
		{
			Scope:     "project",
			BindingID: uuid.New(),
			RoleID:    uuid.New(),
			RoleName:  "project-viewer",
			ProjectID: pgtype.UUID{Bytes: projectID, Valid: true},
			RoleRules: []byte(`[{"resource":"*","verbs":["read","list"]}]`),
		},
	})
	projectNamespacesForTest[projectID.String()] = []sqlc.ProjectNamespace{
		{ProjectID: projectID, ClusterID: clusterID, Namespace: "team-a"},
		{ProjectID: projectID, ClusterID: clusterID, Namespace: "team-b"},
	}
	t.Cleanup(func() { delete(projectNamespacesForTest, projectID.String()) })

	q := NewSQLCRBACQuerierWithCache(fake, nil)
	q.SetNamespaceScoping(true)

	bindings, err := q.GetUserBindings(context.Background(), userID.String())
	if err != nil {
		t.Fatalf("GetUserBindings: %v", err)
	}

	// Original project binding preserved + two synthetic namespace-scoped cluster
	// bindings.
	if len(bindings) != 3 {
		t.Fatalf("binding count = %d, want 3: %+v", len(bindings), bindings)
	}
	engine := rbac.NewEngine()
	all, names := engine.AuthorizedNamespaces(bindings, rbac.ResourcePods, rbac.VerbList, clusterID)
	if all {
		t.Fatal("project member should NOT have cluster-wide visibility")
	}
	if len(names) != 2 {
		t.Fatalf("authorized namespaces = %v, want team-a, team-b", names)
	}
	for _, ns := range []string{"team-a", "team-b"} {
		if _, ok := names[ns]; !ok {
			t.Fatalf("missing authorized namespace %q", ns)
		}
	}

	// With the flag OFF the same rows must NOT expand — project binding grants
	// nothing on the cluster read (regression guard for byte-identical off-path).
	qOff := NewSQLCRBACQuerierWithCache(fake, nil)
	offBindings, err := qOff.GetUserBindings(context.Background(), userID.String())
	if err != nil {
		t.Fatalf("GetUserBindings (off): %v", err)
	}
	if len(offBindings) != 1 {
		t.Fatalf("flag-off binding count = %d, want 1 (no expansion)", len(offBindings))
	}
	if allOff, _ := engine.AuthorizedNamespaces(offBindings, rbac.ResourcePods, rbac.VerbList, clusterID); allOff {
		t.Fatal("flag-off project binding must not grant cluster-wide access")
	}
}
