package middleware

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// projectBindingRow builds the ListUserBindingsWithRoles row a `project-member`
// grant produces: scope "project", only project_id populated, no cluster and no
// namespace. On its own it matches nothing on a cluster resource route.
func projectBindingRow(projectID uuid.UUID, roleName string) sqlc.ListUserBindingsWithRolesRow {
	return sqlc.ListUserBindingsWithRolesRow{
		Scope:     "project",
		BindingID: uuid.New(),
		RoleID:    uuid.New(),
		RoleName:  roleName,
		ProjectID: pgtype.UUID{Bytes: projectID, Valid: true},
		RoleRules: []byte(`[{"resource":"pods","verbs":["read","list"]}]`),
	}
}

// TestGetUserBindings_AlwaysExpandsProjectNamespaces is the parity regression:
// a project binding must resolve to the namespaces its project owns with nothing
// configured. Expansion used to be gated on a flag that defaulted off, so out of
// the box a `project-owner`/`project-member` binding granted nothing at all on
// pods, workloads, namespaces or events.
func TestGetUserBindings_AlwaysExpandsProjectNamespaces(t *testing.T) {
	userID := uuid.New()
	projectID := uuid.New()
	clusterID := uuid.New()

	fake := newFakeUserBindingsQuerier()
	fake.setRows(userID.String(), []sqlc.ListUserBindingsWithRolesRow{projectBindingRow(projectID, "project-member")})
	projectNamespacesForTest[projectID.String()] = []sqlc.ProjectNamespace{
		{ProjectID: projectID, ClusterID: clusterID, Namespace: "team-a"},
		{ProjectID: projectID, ClusterID: clusterID, Namespace: "team-b"},
	}
	t.Cleanup(func() { delete(projectNamespacesForTest, projectID.String()) })

	// No SetNamespaceScoping, no flag, no option: the plain constructor.
	q := NewSQLCRBACQuerierWithCache(fake, nil)
	bindings, err := q.GetUserBindings(context.Background(), userID.String())
	if err != nil {
		t.Fatalf("GetUserBindings: %v", err)
	}

	// The original project binding survives (project-scoped routes still need
	// it) plus one synthetic cluster binding per owned namespace.
	if len(bindings) != 3 {
		t.Fatalf("binding count = %d, want 3 (1 project + 2 synthetic): %+v", len(bindings), bindings)
	}
	synthetic := map[string]rbac.RoleBinding{}
	for _, b := range bindings {
		if b.Scope != "cluster" {
			continue
		}
		if b.ClusterID != clusterID.String() {
			t.Fatalf("synthetic binding cluster = %q, want %q", b.ClusterID, clusterID)
		}
		if b.RoleName != "project-member" {
			t.Fatalf("synthetic binding lost its role: %+v", b)
		}
		synthetic[b.Namespace] = b
	}
	if len(synthetic) != 2 {
		t.Fatalf("synthetic bindings = %v, want team-a + team-b", synthetic)
	}
	for _, ns := range []string{"team-a", "team-b"} {
		if _, ok := synthetic[ns]; !ok {
			t.Fatalf("no synthetic binding for namespace %q", ns)
		}
	}

	engine := rbac.NewEngine()
	all, names := engine.AuthorizedNamespaces(bindings, rbac.ResourcePods, rbac.VerbList, clusterID)
	if all {
		t.Fatal("a project member must NOT resolve to cluster-wide visibility")
	}
	if len(names) != 2 {
		t.Fatalf("authorized namespaces = %v, want exactly team-a + team-b", names)
	}

	// Another cluster's resources stay invisible: the synthetic bindings carry
	// the project's own cluster_id, so nothing crosses to a cluster the project
	// was never materialized on.
	if allOther, otherNames := engine.AuthorizedNamespaces(bindings, rbac.ResourcePods, rbac.VerbList, uuid.New()); allOther || len(otherNames) != 0 {
		t.Fatalf("project binding leaked onto an unrelated cluster: all=%v names=%v", allOther, otherNames)
	}
}

// TestGetUserBindings_ProjectWithNoNamespacesContributesNothing pins the
// fail-closed half: making expansion unconditional must not turn a project with
// no namespace mappings into a broader grant than it had.
func TestGetUserBindings_ProjectWithNoNamespacesContributesNothing(t *testing.T) {
	userID := uuid.New()
	projectID := uuid.New()

	fake := newFakeUserBindingsQuerier()
	fake.setRows(userID.String(), []sqlc.ListUserBindingsWithRolesRow{projectBindingRow(projectID, "project-owner")})
	// Deliberately no projectNamespacesForTest entry: the project owns nothing.

	q := NewSQLCRBACQuerierWithCache(fake, nil)
	bindings, err := q.GetUserBindings(context.Background(), userID.String())
	if err != nil {
		t.Fatalf("GetUserBindings: %v", err)
	}
	if len(bindings) != 1 || bindings[0].Scope != "project" {
		t.Fatalf("bindings = %+v, want just the original project binding", bindings)
	}

	engine := rbac.NewEngine()
	all, names := engine.AuthorizedNamespaces(bindings, rbac.ResourcePods, rbac.VerbList, uuid.New())
	if all {
		t.Fatal("an unmapped project must not grant cluster-wide access")
	}
	if len(names) != 0 {
		t.Fatalf("authorized namespaces = %v, want none", names)
	}
}

// TestGetUserBindings_BlankNamespaceRowIsNotAClusterWideGrant guards the worst
// possible expansion bug. AuthorizedNamespaces treats a cluster binding with an
// empty Namespace as "the whole cluster", so a project_namespaces row with a
// blank namespace (the column is NOT NULL, not non-empty) would silently promote
// a single-namespace project grant into cluster-wide read.
func TestGetUserBindings_BlankNamespaceRowIsNotAClusterWideGrant(t *testing.T) {
	userID := uuid.New()
	projectID := uuid.New()
	clusterID := uuid.New()

	fake := newFakeUserBindingsQuerier()
	fake.setRows(userID.String(), []sqlc.ListUserBindingsWithRolesRow{projectBindingRow(projectID, "project-member")})
	projectNamespacesForTest[projectID.String()] = []sqlc.ProjectNamespace{
		{ProjectID: projectID, ClusterID: clusterID, Namespace: ""},
		{ProjectID: projectID, ClusterID: uuid.Nil, Namespace: "team-a"},
		{ProjectID: projectID, ClusterID: clusterID, Namespace: "team-a"},
	}
	t.Cleanup(func() { delete(projectNamespacesForTest, projectID.String()) })

	q := NewSQLCRBACQuerierWithCache(fake, nil)
	bindings, err := q.GetUserBindings(context.Background(), userID.String())
	if err != nil {
		t.Fatalf("GetUserBindings: %v", err)
	}
	for _, b := range bindings {
		if b.Scope == "cluster" && (b.Namespace == "" || b.ClusterID == "" || b.ClusterID == uuid.Nil.String()) {
			t.Fatalf("degenerate row expanded into a wide grant: %+v", b)
		}
	}

	engine := rbac.NewEngine()
	all, names := engine.AuthorizedNamespaces(bindings, rbac.ResourcePods, rbac.VerbList, clusterID)
	if all {
		t.Fatal("blank-namespace row produced a cluster-wide grant")
	}
	if len(names) != 1 {
		t.Fatalf("authorized namespaces = %v, want only team-a", names)
	}
	if _, ok := names["team-a"]; !ok {
		t.Fatalf("authorized namespaces = %v, want team-a", names)
	}
}

// TestGetUserBindings_RemovedNamespaceStopsAuthorizingAfterInvalidate is the
// revocation guard. project_namespaces membership is an input to a CACHED authz
// decision, so removing a namespace from a project has to flush the cache or the
// removed namespace keeps authorizing reads until the entry expires on its own.
// ProjectHandler.AddNamespace/RemoveNamespace call InvalidateAll for exactly
// this; the wiring that makes those calls reach a real querier is asserted by
// TestValidateProductionSecurityWiring in internal/server.
func TestGetUserBindings_RemovedNamespaceStopsAuthorizingAfterInvalidate(t *testing.T) {
	userID := uuid.New()
	projectID := uuid.New()
	clusterID := uuid.New()

	fake := newFakeUserBindingsQuerier()
	fake.setRows(userID.String(), []sqlc.ListUserBindingsWithRolesRow{projectBindingRow(projectID, "project-member")})
	projectNamespacesForTest[projectID.String()] = []sqlc.ProjectNamespace{
		{ProjectID: projectID, ClusterID: clusterID, Namespace: "team-a"},
	}
	t.Cleanup(func() { delete(projectNamespacesForTest, projectID.String()) })

	q := NewSQLCRBACQuerierWithCache(fake, NewRBACCache())
	engine := rbac.NewEngine()

	bindings, err := q.GetUserBindings(context.Background(), userID.String())
	if err != nil {
		t.Fatalf("GetUserBindings: %v", err)
	}
	if _, names := engine.AuthorizedNamespaces(bindings, rbac.ResourcePods, rbac.VerbList, clusterID); len(names) != 1 {
		t.Fatalf("pre-removal authorized namespaces = %v, want team-a", names)
	}

	// Admin removes team-a from the project. Without the flush the cached
	// synthetic binding keeps granting it.
	delete(projectNamespacesForTest, projectID.String())
	cached, err := q.GetUserBindings(context.Background(), userID.String())
	if err != nil {
		t.Fatalf("GetUserBindings (cached): %v", err)
	}
	if _, names := engine.AuthorizedNamespaces(cached, rbac.ResourcePods, rbac.VerbList, clusterID); len(names) != 1 {
		t.Fatalf("cache unexpectedly bypassed; got %v", names)
	}

	q.InvalidateAll()
	after, err := q.GetUserBindings(context.Background(), userID.String())
	if err != nil {
		t.Fatalf("GetUserBindings (post-invalidate): %v", err)
	}
	all, names := engine.AuthorizedNamespaces(after, rbac.ResourcePods, rbac.VerbList, clusterID)
	if all || len(names) != 0 {
		t.Fatalf("revoked namespace still authorized after flush: all=%v names=%v", all, names)
	}
}
