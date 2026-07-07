package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

func clusterUpdateRules() []rbac.Rule {
	return []rbac.Rule{{Resource: "clusters", Verbs: []string{"read", "update", "delete"}}}
}

func TestDeriveCallerScope(t *testing.T) {
	engine := rbac.NewEngine()
	cluster := uuid.New()
	otherCluster := uuid.New()
	caller := uuid.New()
	rw := kubectl.EffectiveVerbs{Read: true, Update: true, Delete: true}

	t.Run("nil engine fails closed", func(t *testing.T) {
		s := deriveCallerScope(nil, nil, cluster, caller, rw)
		if s.Determined {
			t.Fatalf("nil engine must yield an undetermined scope")
		}
	})

	t.Run("no applicable binding fails closed", func(t *testing.T) {
		bindings := []rbac.RoleBinding{
			{UserID: caller.String(), RoleRules: clusterUpdateRules(), ClusterID: otherCluster.String()},
		}
		s := deriveCallerScope(engine, bindings, cluster, caller, rw)
		if s.Determined {
			t.Fatalf("binding for a different cluster must not determine scope for this cluster")
		}
	})

	t.Run("superuser gets full cluster and requested verbs", func(t *testing.T) {
		bindings := []rbac.RoleBinding{{UserID: caller.String(), IsSuperuser: true}}
		s := deriveCallerScope(engine, bindings, cluster, caller, rw)
		if !s.Determined || !s.AllNamespaces || !s.Superuser {
			t.Fatalf("superuser scope = %+v", s)
		}
		if s.Verbs != rw {
			t.Fatalf("superuser verbs = %+v, want %+v", s.Verbs, rw)
		}
		if s.ImpersonationHeaders() != nil {
			t.Fatalf("superuser scope must not impersonate")
		}
	})

	t.Run("cluster-wide binding keeps write and cross-namespace", func(t *testing.T) {
		bindings := []rbac.RoleBinding{
			{UserID: caller.String(), RoleRules: clusterUpdateRules(), ClusterID: cluster.String()},
		}
		s := deriveCallerScope(engine, bindings, cluster, caller, rw)
		if !s.Determined || !s.AllNamespaces {
			t.Fatalf("cluster-wide scope = %+v", s)
		}
		if !s.Verbs.Read || !s.Verbs.Update || !s.Verbs.Delete {
			t.Fatalf("cluster-wide verbs should include write, got %+v", s.Verbs)
		}
	})

	t.Run("namespace-scoped binding is confined and read-only", func(t *testing.T) {
		bindings := []rbac.RoleBinding{
			{UserID: caller.String(), RoleRules: clusterUpdateRules(), ClusterID: cluster.String(), Namespace: "team-a"},
		}
		s := deriveCallerScope(engine, bindings, cluster, caller, rw)
		if !s.Determined || s.AllNamespaces {
			t.Fatalf("namespace scope = %+v", s)
		}
		if _, ok := s.Namespaces["team-a"]; !ok {
			t.Fatalf("expected team-a in namespaces, got %+v", s.SortedNamespaces())
		}
		// Coarse ClusterRole can't express per-namespace write → read-only.
		if !s.Verbs.Read || s.Verbs.Update || s.Verbs.Delete {
			t.Fatalf("namespace-scoped verbs must be read-only, got %+v", s.Verbs)
		}
		if !s.Allows("team-a") || s.Allows("team-b") || s.Allows(namespaceAllSentinel) {
			t.Fatalf("Allows semantics wrong for %+v", s)
		}
		if s.Allows("") == false {
			t.Fatalf("empty (default) namespace should be allowed")
		}
		if s.ImpersonationHeaders()["Impersonate-User"] != "astronomer:user:"+caller.String() {
			t.Fatalf("impersonation header = %v", s.ImpersonationHeaders())
		}
	})

	t.Run("read-only request is not elevated even cluster-wide", func(t *testing.T) {
		bindings := []rbac.RoleBinding{
			{UserID: caller.String(), RoleRules: clusterUpdateRules(), ClusterID: cluster.String()},
		}
		s := deriveCallerScope(engine, bindings, cluster, caller, kubectl.EffectiveVerbs{Read: true})
		if s.Verbs.Update || s.Verbs.Delete {
			t.Fatalf("requested read-only must stay read-only, got %+v", s.Verbs)
		}
	})
}

// podsRules grants pods:get/list/watch so a binding's role matches the
// resource/verb deriveCallerScope resolves namespaces against.
func podsRules() []rbac.Rule {
	return []rbac.Rule{{Resource: "pods", Verbs: []string{"get", "list", "watch"}}}
}

// TestDeriveCallerScope_ProjectExpansion covers the DIR-04 change: a RAW
// project binding is no longer treated as a cross-namespace grant, and the
// project→namespace synthetic cluster bindings (what GetUserBindings emits
// when namespace_scoped_rbac is on) resolve into a concrete namespace
// allow-set via engine.AuthorizedNamespaces.
func TestDeriveCallerScope_ProjectExpansion(t *testing.T) {
	engine := rbac.NewEngine()
	cluster := uuid.New()
	caller := uuid.New()
	project := uuid.New()
	rw := kubectl.EffectiveVerbs{Read: true, Update: true, Delete: true}

	t.Run("raw project binding is not cross-namespace and fails closed unexpanded", func(t *testing.T) {
		// A project binding with no synthetic expansion present resolves to
		// zero namespaces → undetermined → fail closed. Crucially it must
		// NOT grant AllNamespaces (the old over-grant).
		bindings := []rbac.RoleBinding{
			{UserID: caller.String(), RoleRules: podsRules(), Scope: "project", ProjectID: project.String()},
		}
		s := deriveCallerScope(engine, bindings, cluster, caller, rw)
		if s.AllNamespaces {
			t.Fatalf("raw project binding must not grant cross-namespace scope, got %+v", s)
		}
		if s.Determined {
			t.Fatalf("unexpanded project-only binding must fail closed (undetermined), got %+v", s)
		}
	})

	t.Run("project expansion resolves concrete namespaces confined", func(t *testing.T) {
		// The original project binding PLUS the two synthetic cluster+namespace
		// bindings GetUserBindings would append for this project's namespaces.
		bindings := []rbac.RoleBinding{
			{UserID: caller.String(), RoleRules: podsRules(), Scope: "project", ProjectID: project.String()},
			{UserID: caller.String(), RoleRules: podsRules(), Scope: "cluster", ClusterID: cluster.String(), Namespace: "team-a"},
			{UserID: caller.String(), RoleRules: podsRules(), Scope: "cluster", ClusterID: cluster.String(), Namespace: "team-b"},
		}
		s := deriveCallerScope(engine, bindings, cluster, caller, rw)
		if !s.Determined || s.AllNamespaces {
			t.Fatalf("expected confined determined scope, got %+v", s)
		}
		if _, ok := s.Namespaces["team-a"]; !ok {
			t.Fatalf("team-a missing from %v", s.SortedNamespaces())
		}
		if _, ok := s.Namespaces["team-b"]; !ok {
			t.Fatalf("team-b missing from %v", s.SortedNamespaces())
		}
		if s.Allows("team-c") {
			t.Fatalf("team-c must be out of scope")
		}
		// Confinement caps at read-only (coarse ClusterRole can't express a
		// per-namespace write; mechanism A provisions read-only Roles).
		if s.Verbs.Update || s.Verbs.Delete {
			t.Fatalf("confined scope must be read-only, got %+v", s.Verbs)
		}
	})
}

// TestShellScopeEnabled_FlagCollapse guards §4.3: shell caller-scoping turns
// on when the master namespace_scoped_rbac switch is on, without needing the
// legacy feature.shell_scope_to_caller platform setting.
func TestShellScopeEnabled_FlagCollapse(t *testing.T) {
	t.Run("namespace-scoped RBAC on enables scoping with no feature reader", func(t *testing.T) {
		h := &KubectlShellHandler{NamespaceScopedRBAC: true}
		if !h.shellScopeEnabled(context.Background()) {
			t.Fatal("NamespaceScopedRBAC=true must enable shell scoping")
		}
	})
	t.Run("both off keeps scoping off", func(t *testing.T) {
		h := &KubectlShellHandler{}
		if h.shellScopeEnabled(context.Background()) {
			t.Fatal("with both switches off, scoping must stay off")
		}
	})
}

func TestNamespaceTargetsFromCommand(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"kubectl get pods", nil},
		{"kubectl get pods -n team-a", []string{"team-a"}},
		{"kubectl get pods --namespace team-b", []string{"team-b"}},
		{"kubectl get pods --namespace=team-c", []string{"team-c"}},
		{"kubectl get pods -n=team-d", []string{"team-d"}},
		{"kubectl get pods -A", []string{namespaceAllSentinel}},
		{"kubectl get pods --all-namespaces", []string{namespaceAllSentinel}},
		{"kubectl get pods -n a -n b", []string{"a", "b"}},
	}
	for _, c := range cases {
		got := namespaceTargetsFromCommand(c.line)
		if len(got) != len(c.want) {
			t.Fatalf("%q → %v, want %v", c.line, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%q → %v, want %v", c.line, got, c.want)
			}
		}
	}
}
