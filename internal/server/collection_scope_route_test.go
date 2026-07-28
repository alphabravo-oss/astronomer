package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// The fleet landing page is the first surface a scoped multi-tenant persona
// hits. Before the collection gate was relaxed, GET /clusters/ and
// /projects/ ran their permission check at uuid.Nil scope, where only a GLOBAL
// binding can match — so a user whose sole grant was a cluster_role_bindings
// row got a flat 403 and the page was unusable. These tests pin the relaxed
// gate AND the per-object filter that makes relaxing it safe.

// scopeFilterClusterQuerier is an in-memory ClusterQuerier that serves both the
// unfiltered and the scope-filtered list queries so a route test can assert
// which rows (and which count) a caller actually gets.
type scopeFilterClusterQuerier struct {
	routeSecurityClusterQuerier
	clusters []sqlc.Cluster
}

func (q *scopeFilterClusterQuerier) ListClusters(_ context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	return pageClusters(q.clusters, arg.Limit, arg.Offset), nil
}

func (q *scopeFilterClusterQuerier) CountClusters(context.Context) (int64, error) {
	return int64(len(q.clusters)), nil
}

func (q *scopeFilterClusterQuerier) ListClustersForScopes(_ context.Context, arg sqlc.ListClustersForScopesParams) ([]sqlc.Cluster, error) {
	return pageClusters(filterClustersByID(q.clusters, arg.ClusterIds), arg.QueryLimit, arg.QueryOffset), nil
}

func (q *scopeFilterClusterQuerier) CountClustersForScopes(_ context.Context, ids []uuid.UUID) (int64, error) {
	return int64(len(filterClustersByID(q.clusters, ids))), nil
}

func filterClustersByID(clusters []sqlc.Cluster, ids []uuid.UUID) []sqlc.Cluster {
	allow := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		allow[id] = struct{}{}
	}
	out := make([]sqlc.Cluster, 0, len(clusters))
	for _, c := range clusters {
		if _, ok := allow[c.ID]; ok {
			out = append(out, c)
		}
	}
	return out
}

func pageClusters(clusters []sqlc.Cluster, limit, offset int32) []sqlc.Cluster {
	if int(offset) >= len(clusters) {
		return nil
	}
	end := int(offset) + int(limit)
	if end > len(clusters) {
		end = len(clusters)
	}
	return clusters[offset:end]
}

func pageProjects(projects []sqlc.Project, limit, offset int32) []sqlc.Project {
	if int(offset) >= len(projects) {
		return nil
	}
	end := int(offset) + int(limit)
	if end > len(projects) {
		end = len(projects)
	}
	return projects[offset:end]
}

// scopeFilterProjectQuerier embeds the ProjectQuerier interface as a nil value:
// only the list path is exercised, and any other call would panic loudly rather
// than silently return a zero row.
type scopeFilterProjectQuerier struct {
	handler.ProjectQuerier
	projects []sqlc.Project
}

func (q *scopeFilterProjectQuerier) ListProjects(_ context.Context, arg sqlc.ListProjectsParams) ([]sqlc.Project, error) {
	return pageProjects(q.projects, arg.Limit, arg.Offset), nil
}

func (q *scopeFilterProjectQuerier) CountProjects(context.Context) (int64, error) {
	return int64(len(q.projects)), nil
}

func (q *scopeFilterProjectQuerier) ListProjectsForScopes(_ context.Context, arg sqlc.ListProjectsForScopesParams) ([]sqlc.Project, error) {
	return pageProjects(filterProjectsByScope(q.projects, arg.ProjectIds, arg.ClusterIds), arg.QueryLimit, arg.QueryOffset), nil
}

func (q *scopeFilterProjectQuerier) CountProjectsForScopes(_ context.Context, arg sqlc.CountProjectsForScopesParams) (int64, error) {
	return int64(len(filterProjectsByScope(q.projects, arg.ProjectIds, arg.ClusterIds))), nil
}

// filterProjectsByScope mirrors ListProjectsForScopes' predicate: bound
// directly OR living on a cluster the caller holds the grant over.
func filterProjectsByScope(projects []sqlc.Project, projectIDs, clusterIDs []uuid.UUID) []sqlc.Project {
	allowProject := make(map[uuid.UUID]struct{}, len(projectIDs))
	for _, id := range projectIDs {
		allowProject[id] = struct{}{}
	}
	allowCluster := make(map[uuid.UUID]struct{}, len(clusterIDs))
	for _, id := range clusterIDs {
		allowCluster[id] = struct{}{}
	}
	out := make([]sqlc.Project, 0, len(projects))
	for _, p := range projects {
		if _, ok := allowProject[p.ID]; ok {
			out = append(out, p)
			continue
		}
		if _, ok := allowCluster[p.ClusterID]; ok {
			out = append(out, p)
		}
	}
	return out
}

func newCollectionScopeRouter(t *testing.T, bindings []rbac.RoleBinding, clusters []sqlc.Cluster, projects []sqlc.Project) (http.Handler, string) {
	t.Helper()
	jwtMgr := auth.NewJWTManager("collection-scope-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterHandler := handler.NewClusterHandler(&scopeFilterClusterQuerier{clusters: clusters})
	projectHandler := handler.NewProjectHandler(&scopeFilterProjectQuerier{projects: projects})
	engine := rbac.NewEngine()
	querier := routeSecurityRBACQuerier{bindings: bindings}
	clusterHandler.SetAuthorization(engine, querier)
	projectHandler.SetAuthorization(engine, querier)
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  engine,
		RBACQueries: querier,
		Clusters:    clusterHandler,
		Projects:    projectHandler,
	})
	return router, token
}

type collectionPage struct {
	Data  []map[string]any `json:"data"`
	Count int64            `json:"count"`
}

func getCollection(t *testing.T, router http.Handler, path, token string) (int, collectionPage) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	page := collectionPage{}
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode %s: %v; body=%s", path, err, rec.Body.String())
		}
	}
	return rec.Code, page
}

func collectionTestClusters(n int) []sqlc.Cluster {
	clusters := make([]sqlc.Cluster, 0, n)
	for i := 0; i < n; i++ {
		clusters = append(clusters, sqlc.Cluster{ID: uuid.New(), Name: "cluster", DisplayName: "cluster"})
	}
	return clusters
}

// clusterScopedListBindings models the shipping persona: one
// cluster_role_bindings row with a Cluster Viewer-shaped role.
func clusterScopedListBindings(clusterIDs ...uuid.UUID) []rbac.RoleBinding {
	bindings := make([]rbac.RoleBinding, 0, len(clusterIDs))
	for _, id := range clusterIDs {
		bindings = append(bindings, rbac.RoleBinding{
			Scope:     "cluster",
			ClusterID: id.String(),
			RoleRules: []rbac.Rule{{Resource: "*", Verbs: []string{"read", "list", "watch"}}},
		})
	}
	return bindings
}

// globalReadOnlyListBindings is the seeded 'Read Only' global role: a
// platform-wide grant, so it must keep seeing the entire fleet.
func globalReadOnlyListBindings() []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		Scope:     "global",
		RoleRules: []rbac.Rule{{Resource: "*", Verbs: []string{"read", "list"}}},
	}}
}

// TestClusterCollectionIsScopeFilteredForClusterBoundCaller pins the core fix:
// a caller holding ONE cluster_role_bindings row reaches the fleet list (403
// before) and sees exactly that cluster — not the whole fleet.
func TestClusterCollectionIsScopeFilteredForClusterBoundCaller(t *testing.T) {
	clusters := collectionTestClusters(5)
	mine := clusters[2]
	router, token := newCollectionScopeRouter(t, clusterScopedListBindings(mine.ID), clusters, nil)

	code, page := getCollection(t, router, "/api/v1/clusters/", token)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want %d", code, http.StatusOK)
	}
	if len(page.Data) != 1 {
		t.Fatalf("clusters returned = %d, want 1; page=%+v", len(page.Data), page)
	}
	if page.Data[0]["id"] != mine.ID.String() {
		t.Fatalf("cluster id = %v, want %s", page.Data[0]["id"], mine.ID)
	}
	if page.Count != 1 {
		t.Fatalf("count = %d, want 1 (the filtered total, not the fleet size)", page.Count)
	}
}

// TestProjectCollectionIsScopeFilteredForScopedCaller is the /projects/ twin:
// a project binding shows that project, and a cluster binding shows every
// project on that cluster.
func TestProjectCollectionIsScopeFilteredForScopedCaller(t *testing.T) {
	clusterA, clusterB := uuid.New(), uuid.New()
	onA := sqlc.Project{ID: uuid.New(), Name: "on-a", ClusterID: clusterA}
	alsoOnA := sqlc.Project{ID: uuid.New(), Name: "also-on-a", ClusterID: clusterA}
	onB := sqlc.Project{ID: uuid.New(), Name: "on-b", ClusterID: clusterB}
	projects := []sqlc.Project{onA, alsoOnA, onB}

	t.Run("project binding sees only its project", func(t *testing.T) {
		bindings := []rbac.RoleBinding{{
			Scope:     "project",
			ProjectID: onB.ID.String(),
			RoleRules: []rbac.Rule{{Resource: "*", Verbs: []string{"read", "list"}}},
		}}
		router, token := newCollectionScopeRouter(t, bindings, nil, projects)
		code, page := getCollection(t, router, "/api/v1/projects/", token)
		if code != http.StatusOK {
			t.Fatalf("status = %d, want %d", code, http.StatusOK)
		}
		if len(page.Data) != 1 || page.Data[0]["id"] != onB.ID.String() {
			t.Fatalf("projects = %+v, want only %s", page.Data, onB.ID)
		}
		if page.Count != 1 {
			t.Fatalf("count = %d, want 1", page.Count)
		}
	})

	t.Run("cluster binding sees that cluster's projects", func(t *testing.T) {
		router, token := newCollectionScopeRouter(t, clusterScopedListBindings(clusterA), nil, projects)
		code, page := getCollection(t, router, "/api/v1/projects/", token)
		if code != http.StatusOK {
			t.Fatalf("status = %d, want %d", code, http.StatusOK)
		}
		if len(page.Data) != 2 {
			t.Fatalf("projects returned = %d, want 2; page=%+v", len(page.Data), page)
		}
		if page.Count != 2 {
			t.Fatalf("count = %d, want 2", page.Count)
		}
	})
}

// TestNamespaceNarrowedBindingDoesNotSeeNeighbouringProjects is the
// multi-tenant pin. A namespace-narrowed CLUSTER binding must not widen the
// /projects/ page to every project on that cluster: those rows belong to other
// tenants and GET /projects/{id}/ 403s the caller on each of them, so listing
// them is pure metadata disclosure. The shape is not exotic —
// expandProjectBindings emits exactly this binding for EVERY project binding
// once namespace-scoped RBAC is on, alongside the original project binding, so
// this is the normal resolved shape of a project-confined tenant.
//
// /clusters/ deliberately keeps the opposite policy (see the second subtest):
// reading something inside a cluster already implies knowing it exists.
func TestNamespaceNarrowedBindingDoesNotSeeNeighbouringProjects(t *testing.T) {
	cluster := sqlc.Cluster{ID: uuid.New(), Name: "shared", DisplayName: "shared"}
	mine := sqlc.Project{ID: uuid.New(), Name: "mine", ClusterID: cluster.ID}
	neighbour := sqlc.Project{ID: uuid.New(), Name: "other-tenant", ClusterID: cluster.ID}
	projects := []sqlc.Project{mine, neighbour}

	// What expandProjectBindings produces for a Project Viewer on `mine`: the
	// original project binding plus a namespace-narrowed cluster binding.
	rules := []rbac.Rule{{Resource: "*", Verbs: []string{"read", "list", "watch"}}}
	bindings := []rbac.RoleBinding{
		{Scope: "project", ProjectID: mine.ID.String(), RoleRules: rules},
		{Scope: "cluster", ClusterID: cluster.ID.String(), Namespace: "team-a", RoleRules: rules},
	}
	router, token := newCollectionScopeRouter(t, bindings, []sqlc.Cluster{cluster}, projects)

	code, page := getCollection(t, router, "/api/v1/projects/", token)
	if code != http.StatusOK {
		t.Fatalf("projects status = %d, want %d", code, http.StatusOK)
	}
	if len(page.Data) != 1 || page.Data[0]["id"] != mine.ID.String() {
		t.Fatalf("projects = %+v, want only %s (%q must not be listed)", page.Data, mine.ID, neighbour.Name)
	}
	if page.Count != 1 {
		t.Fatalf("count = %d, want 1", page.Count)
	}

	// The same narrowed binding still names its cluster on /clusters/.
	code, page = getCollection(t, router, "/api/v1/clusters/", token)
	if code != http.StatusOK {
		t.Fatalf("clusters status = %d, want %d", code, http.StatusOK)
	}
	if len(page.Data) != 1 || page.Data[0]["id"] != cluster.ID.String() {
		t.Fatalf("clusters = %+v, want the narrowed binding's own cluster %s", page.Data, cluster.ID)
	}
}

// TestGlobalBindingStillSeesEntireFleet is the no-regression half of the
// compatibility rule: a platform-wide grant is by definition fleet-wide, and
// filtering must not quietly turn it into a privilege reduction.
func TestGlobalBindingStillSeesEntireFleet(t *testing.T) {
	clusters := collectionTestClusters(7)
	projects := []sqlc.Project{
		{ID: uuid.New(), Name: "p1", ClusterID: clusters[0].ID},
		{ID: uuid.New(), Name: "p2", ClusterID: clusters[1].ID},
	}
	router, token := newCollectionScopeRouter(t, globalReadOnlyListBindings(), clusters, projects)

	code, page := getCollection(t, router, "/api/v1/clusters/", token)
	if code != http.StatusOK {
		t.Fatalf("clusters status = %d, want %d", code, http.StatusOK)
	}
	if len(page.Data) != 7 || page.Count != 7 {
		t.Fatalf("clusters = %d (count %d), want 7/7", len(page.Data), page.Count)
	}

	code, page = getCollection(t, router, "/api/v1/projects/", token)
	if code != http.StatusOK {
		t.Fatalf("projects status = %d, want %d", code, http.StatusOK)
	}
	if len(page.Data) != 2 || page.Count != 2 {
		t.Fatalf("projects = %d (count %d), want 2/2", len(page.Data), page.Count)
	}
}

// TestSuperuserStillSeesEntireFleet pins the other unrestricted persona: the
// synthetic IsSuperuser binding carries no rules at all, so it must be caught
// by the short-circuit rather than filtered down to an empty page.
func TestSuperuserStillSeesEntireFleet(t *testing.T) {
	clusters := collectionTestClusters(4)
	router, token := newCollectionScopeRouter(t, []rbac.RoleBinding{{IsSuperuser: true}}, clusters, nil)

	code, page := getCollection(t, router, "/api/v1/clusters/", token)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want %d", code, http.StatusOK)
	}
	if len(page.Data) != 4 || page.Count != 4 {
		t.Fatalf("clusters = %d (count %d), want 4/4", len(page.Data), page.Count)
	}
}

// TestScopedCollectionCountMatchesFilteredSetAcrossPages is the pager half: a
// filtered page under an unfiltered total leaks the fleet size AND emits a
// `next` link to rows that never arrive. The fixture is deliberately larger
// than one page so page 2 exists.
func TestScopedCollectionCountMatchesFilteredSetAcrossPages(t *testing.T) {
	clusters := collectionTestClusters(9)
	mine := []uuid.UUID{clusters[1].ID, clusters[4].ID, clusters[7].ID}
	router, token := newCollectionScopeRouter(t, clusterScopedListBindings(mine...), clusters, nil)

	code, first := getCollection(t, router, "/api/v1/clusters/?limit=2&offset=0", token)
	if code != http.StatusOK {
		t.Fatalf("page 1 status = %d, want %d", code, http.StatusOK)
	}
	if len(first.Data) != 2 {
		t.Fatalf("page 1 rows = %d, want 2", len(first.Data))
	}
	if first.Count != 3 {
		t.Fatalf("page 1 count = %d, want 3 (filtered total, not the 9-cluster fleet)", first.Count)
	}

	code, second := getCollection(t, router, "/api/v1/clusters/?limit=2&offset=2", token)
	if code != http.StatusOK {
		t.Fatalf("page 2 status = %d, want %d", code, http.StatusOK)
	}
	if len(second.Data) != 1 {
		t.Fatalf("page 2 rows = %d, want 1", len(second.Data))
	}
	if second.Count != 3 {
		t.Fatalf("page 2 count = %d, want 3", second.Count)
	}
	// The two pages together must be exactly the caller's three clusters.
	seen := map[string]struct{}{}
	for _, row := range append(append([]map[string]any{}, first.Data...), second.Data...) {
		seen[row["id"].(string)] = struct{}{}
	}
	for _, id := range mine {
		if _, ok := seen[id.String()]; !ok {
			t.Fatalf("cluster %s missing from the paged result; seen=%v", id, seen)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("distinct clusters across pages = %d, want 3", len(seen))
	}
}

// TestCollectionWithoutAnyRelevantBindingIsStillForbidden documents the chosen
// no-binding semantics: 403, not an empty 200. The relaxed gate only widens
// WHERE a clusters:list grant may live, never whether one is required, so a
// caller holding no such grant at any scope is denied exactly as before. An
// empty 200 would be a silent contract change and would tell the caller the
// fleet is empty rather than that they may not list it.
func TestCollectionWithoutAnyRelevantBindingIsStillForbidden(t *testing.T) {
	clusters := collectionTestClusters(3)
	projects := []sqlc.Project{{ID: uuid.New(), Name: "p1", ClusterID: clusters[0].ID}}
	// A cluster-scoped grant on an unrelated resource: real binding, wrong verb.
	bindings := []rbac.RoleBinding{{
		Scope:     "cluster",
		ClusterID: clusters[0].ID.String(),
		RoleRules: []rbac.Rule{{Resource: "monitoring", Verbs: []string{"read"}}},
	}}
	router, token := newCollectionScopeRouter(t, bindings, clusters, projects)

	for _, path := range []string{"/api/v1/clusters/", "/api/v1/projects/"} {
		if code, _ := getCollection(t, router, path, token); code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want %d", path, code, http.StatusForbidden)
		}
	}
}

// TestCollectionFailsClosedWhenHandlerAuthorizationUnwired pins the wiring
// invariant: the gate is registered in routes_clusters.go but the filter lives
// on the handler, so forgetting SetAuthorization would hand a cluster-scoped
// caller the entire fleet. It must 500 instead.
func TestCollectionFailsClosedWhenHandlerAuthorizationUnwired(t *testing.T) {
	clusters := collectionTestClusters(3)
	jwtMgr := auth.NewJWTManager("collection-scope-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	querier := routeSecurityRBACQuerier{bindings: clusterScopedListBindings(clusters[0].ID)}
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: querier,
		// Deliberately no SetAuthorization on the handler.
		Clusters: handler.NewClusterHandler(&scopeFilterClusterQuerier{clusters: clusters}),
	})

	code, page := getCollection(t, router, "/api/v1/clusters/", token)
	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (fail closed); page=%+v", code, http.StatusInternalServerError, page)
	}
}

// TestScopedCallerSeesEmptyPageWhenGrantedClusterIsGone covers the one case
// where a 200 with no rows is correct: the gate passes (the binding exists) but
// the cluster it names no longer does.
func TestScopedCallerSeesEmptyPageWhenGrantedClusterIsGone(t *testing.T) {
	clusters := collectionTestClusters(3)
	router, token := newCollectionScopeRouter(t, clusterScopedListBindings(uuid.New()), clusters, nil)

	code, page := getCollection(t, router, "/api/v1/clusters/", token)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want %d", code, http.StatusOK)
	}
	if len(page.Data) != 0 || page.Count != 0 {
		t.Fatalf("clusters = %d (count %d), want 0/0", len(page.Data), page.Count)
	}
}
