package rbac

import (
	"testing"

	"github.com/google/uuid"
)

func listRules(resource string) []Rule {
	return []Rule{{Resource: resource, Verbs: []string{"read", "list"}}}
}

// TestAuthorizedScopeIDsCollectsPerScopeGrants pins the allow-set derivation the
// collection list handlers filter on.
func TestAuthorizedScopeIDsCollectsPerScopeGrants(t *testing.T) {
	engine := NewEngine()
	clusterA, clusterB, projectA := uuid.New(), uuid.New(), uuid.New()

	cases := []struct {
		name         string
		bindings     []RoleBinding
		wantAll      bool
		wantClusters []uuid.UUID
		wantProjects []uuid.UUID
	}{
		{
			name:     "superuser sees everything",
			bindings: []RoleBinding{{IsSuperuser: true}},
			wantAll:  true,
		},
		{
			name:     "global grant sees everything",
			bindings: []RoleBinding{{RoleRules: listRules("*")}},
			wantAll:  true,
		},
		{
			name: "cluster bindings collect their clusters",
			bindings: []RoleBinding{
				{Scope: "cluster", ClusterID: clusterA.String(), RoleRules: listRules("clusters")},
				{Scope: "cluster", ClusterID: clusterB.String(), RoleRules: listRules("*")},
			},
			wantClusters: []uuid.UUID{clusterA, clusterB},
		},
		{
			name: "project binding collects only its project",
			bindings: []RoleBinding{
				{Scope: "project", ProjectID: projectA.String(), RoleRules: listRules("*")},
			},
			wantProjects: []uuid.UUID{projectA},
		},
		{
			name: "a binding without the verb contributes nothing",
			bindings: []RoleBinding{
				{Scope: "cluster", ClusterID: clusterA.String(), RoleRules: []Rule{{Resource: "monitoring", Verbs: []string{"read"}}}},
			},
		},
		{
			name: "namespace-narrowed cluster binding still names its cluster",
			bindings: []RoleBinding{
				{Scope: "cluster", ClusterID: clusterA.String(), Namespace: "team-a", RoleRules: listRules("*")},
			},
			wantClusters: []uuid.UUID{clusterA},
		},
		{
			// A namespace on a global binding is invalid; bindingApplies fails it
			// closed and so must this, otherwise it would widen to the whole fleet.
			name: "namespace-narrowed global binding fails closed",
			bindings: []RoleBinding{
				{Namespace: "team-a", RoleRules: listRules("*")},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			all, clusterIDs, projectIDs := engine.AuthorizedScopeIDs(tc.bindings, ResourceClusters, VerbList, NarrowedClustersWiden)
			if all != tc.wantAll {
				t.Fatalf("all = %v, want %v", all, tc.wantAll)
			}
			if all {
				return
			}
			assertIDSet(t, "clusters", clusterIDs, tc.wantClusters)
			assertIDSet(t, "projects", projectIDs, tc.wantProjects)

			// HasAnyScopedAccess is the gate's fallback: it must admit exactly the
			// callers that have something to see.
			wantAdmit := len(tc.wantClusters)+len(tc.wantProjects) > 0
			if got := engine.HasAnyScopedAccess(tc.bindings, ResourceClusters, VerbList); got != wantAdmit {
				t.Fatalf("HasAnyScopedAccess = %v, want %v", got, wantAdmit)
			}
		})
	}
}

// TestAuthorizedScopeIDsNarrowedClustersExcluded pins the /projects/ policy:
// a namespace-narrowed cluster binding must NOT widen to its whole cluster
// there. Widening would list every sibling project on the cluster — another
// tenant's — to a caller confined to one namespace, and GET /projects/{id}/
// 403s every one of those rows. It is also the normal shape of a project-scoped
// tenant, because expandProjectBindings emits exactly this binding for every
// project binding when namespace-scoped RBAC is on.
func TestAuthorizedScopeIDsNarrowedClustersExcluded(t *testing.T) {
	engine := NewEngine()
	clusterA, projectA := uuid.New(), uuid.New()

	narrowedOnly := []RoleBinding{
		{Scope: "cluster", ClusterID: clusterA.String(), Namespace: "team-a", RoleRules: listRules("*")},
	}
	all, clusterIDs, projectIDs := engine.AuthorizedScopeIDs(narrowedOnly, ResourceProjects, VerbList, NarrowedClustersExcluded)
	if all {
		t.Fatal("narrowed cluster binding resolved to fleet-wide project visibility")
	}
	assertIDSet(t, "clusters", clusterIDs, nil)
	assertIDSet(t, "projects", projectIDs, nil)

	// The project binding that produced that synthetic one survives expansion,
	// so the tenant still sees its OWN project — and only that one.
	withProject := append([]RoleBinding{
		{Scope: "project", ProjectID: projectA.String(), RoleRules: listRules("*")},
	}, narrowedOnly...)
	_, clusterIDs, projectIDs = engine.AuthorizedScopeIDs(withProject, ResourceProjects, VerbList, NarrowedClustersExcluded)
	assertIDSet(t, "clusters", clusterIDs, nil)
	assertIDSet(t, "projects", projectIDs, []uuid.UUID{projectA})

	// An UNNARROWED cluster binding still widens — a Cluster Owner sees their
	// cluster's projects under either policy.
	wide := []RoleBinding{{Scope: "cluster", ClusterID: clusterA.String(), RoleRules: listRules("*")}}
	_, clusterIDs, _ = engine.AuthorizedScopeIDs(wide, ResourceProjects, VerbList, NarrowedClustersExcluded)
	assertIDSet(t, "clusters", clusterIDs, []uuid.UUID{clusterA})

	// And the gate still admits the narrowed caller: narrowing is the filter's
	// job, not the gate's.
	if !engine.HasAnyScopedAccess(narrowedOnly, ResourceProjects, VerbList) {
		t.Fatal("HasAnyScopedAccess denied a namespace-narrowed caller off the collection gate")
	}
}

func assertIDSet(t *testing.T, label string, got map[uuid.UUID]struct{}, want []uuid.UUID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for _, id := range want {
		if _, ok := got[id]; !ok {
			t.Fatalf("%s missing %s; got %v", label, id, got)
		}
	}
}

// TestSeededRolesKeepFleetWideVisibility is the compatibility half of the
// collection-filter change. Before it, the ONLY callers who could pass the
// /clusters/ and /projects/ collection gate were those whose grant matched at
// uuid.Nil scope — i.e. superusers and GLOBAL bindings. Filtering must not
// quietly demote any of them: every seeded built-in role that a global binding
// could carry today must still resolve to all==true (unfiltered).
//
// This is why the change ships WITHOUT a role-catalog migration: no shipped
// role template or seeded built-in role loses an access it has today. The
// change is purely additive at the cluster/project tier, where the previous
// behaviour was a flat 403.
func TestSeededRolesKeepFleetWideVisibility(t *testing.T) {
	engine := NewEngine()
	checked := 0
	for _, role := range loadSeededRoles(t) {
		if role.scope != "global" {
			continue
		}
		bindings := []RoleBinding{{Scope: "global", RoleName: role.name, RoleRules: role.rules}}
		for _, res := range []Resource{ResourceClusters, ResourceProjects} {
			// The pre-change gate, verbatim: CheckPermission at nil scope.
			if !engine.CheckPermission(bindings, res, VerbList, uuid.UUID{}, uuid.UUID{}) {
				continue
			}
			checked++
			all, _, _ := engine.AuthorizedScopeIDs(bindings, res, VerbList, NarrowedClustersWiden)
			if !all {
				t.Fatalf("global role %q lost fleet-wide %s:list visibility", role.name, res)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no seeded global role granted clusters/projects list — fixture is not exercising the contract")
	}
}

// TestSeededClusterRolesNowResolveToTheirCluster is the other half: the seeded
// cluster-scoped roles an operator binds for a single-tenant persona used to be
// 403'd off the collection URL entirely. They must now resolve to exactly the
// cluster they are bound to — never to all==true, which would be the fleet leak.
func TestSeededClusterRolesNowResolveToTheirCluster(t *testing.T) {
	engine := NewEngine()
	clusterID := uuid.New()
	checked := 0
	for _, role := range loadSeededRoles(t) {
		if role.scope != "cluster" {
			continue
		}
		bindings := []RoleBinding{{Scope: "cluster", ClusterID: clusterID.String(), RoleName: role.name, RoleRules: role.rules}}
		all, clusterIDs, _ := engine.AuthorizedScopeIDs(bindings, ResourceClusters, VerbList, NarrowedClustersWiden)
		if all {
			t.Fatalf("cluster role %q resolved to fleet-wide visibility", role.name)
		}
		if len(clusterIDs) == 0 {
			// Not every cluster role grants clusters:list (e.g. a storage-only
			// role); those stay denied, which is correct.
			continue
		}
		checked++
		if _, ok := clusterIDs[clusterID]; !ok || len(clusterIDs) != 1 {
			t.Fatalf("cluster role %q resolved to %v, want exactly {%s}", role.name, clusterIDs, clusterID)
		}
	}
	if checked == 0 {
		t.Fatal("no seeded cluster role granted clusters:list — fixture is not exercising the contract")
	}
}
