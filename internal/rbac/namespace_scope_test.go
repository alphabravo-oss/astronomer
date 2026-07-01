package rbac

import (
	"testing"

	"github.com/google/uuid"
)

// podListRule grants pods:list; used to model namespace/cluster read scopes.
var podListRule = []Rule{{Resource: "pods", Verbs: []string{"list"}}}

func TestAuthorizedNamespaces(t *testing.T) {
	engine := NewEngine()
	clusterA := uuid.New()
	clusterB := uuid.New()

	tests := []struct {
		name      string
		bindings  []RoleBinding
		cluster   uuid.UUID
		wantAll   bool
		wantNames []string
	}{
		{
			name:     "superuser sees everything",
			bindings: []RoleBinding{{IsSuperuser: true}},
			cluster:  clusterA,
			wantAll:  true,
		},
		{
			name:     "global reader sees everything",
			bindings: []RoleBinding{{RoleRules: readOnlyRules}},
			cluster:  clusterA,
			wantAll:  true,
		},
		{
			name:     "cluster-wide reader on matching cluster sees everything",
			bindings: []RoleBinding{{ClusterID: clusterA.String(), RoleRules: podListRule}},
			cluster:  clusterA,
			wantAll:  true,
		},
		{
			name:      "namespace-scoped binding yields only that namespace",
			bindings:  []RoleBinding{{ClusterID: clusterA.String(), Namespace: "team-a", RoleRules: podListRule}},
			cluster:   clusterA,
			wantAll:   false,
			wantNames: []string{"team-a"},
		},
		{
			name: "multiple namespace bindings union",
			bindings: []RoleBinding{
				{ClusterID: clusterA.String(), Namespace: "team-a", RoleRules: podListRule},
				{ClusterID: clusterA.String(), Namespace: "team-b", RoleRules: podListRule},
			},
			cluster:   clusterA,
			wantAll:   false,
			wantNames: []string{"team-a", "team-b"},
		},
		{
			name: "namespace binding on other cluster excluded",
			bindings: []RoleBinding{
				{ClusterID: clusterA.String(), Namespace: "team-a", RoleRules: podListRule},
				{ClusterID: clusterB.String(), Namespace: "team-b", RoleRules: podListRule},
			},
			cluster:   clusterA,
			wantAll:   false,
			wantNames: []string{"team-a"},
		},
		{
			name:      "binding without matching verb grants nothing",
			bindings:  []RoleBinding{{ClusterID: clusterA.String(), Namespace: "team-a", RoleRules: []Rule{{Resource: "pods", Verbs: []string{"delete"}}}}},
			cluster:   clusterA,
			wantAll:   false,
			wantNames: nil,
		},
		{
			name:      "namespace-only binding without cluster fails closed",
			bindings:  []RoleBinding{{Namespace: "team-a", RoleRules: podListRule}},
			cluster:   clusterA,
			wantAll:   false,
			wantNames: nil,
		},
		{
			name:      "raw project binding contributes nothing on cluster read",
			bindings:  []RoleBinding{{ProjectID: uuid.New().String(), RoleRules: podListRule}},
			cluster:   clusterA,
			wantAll:   false,
			wantNames: nil,
		},
		{
			name: "cluster-wide grant subsumes namespace grant",
			bindings: []RoleBinding{
				{ClusterID: clusterA.String(), Namespace: "team-a", RoleRules: podListRule},
				{ClusterID: clusterA.String(), RoleRules: podListRule},
			},
			cluster: clusterA,
			wantAll: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			all, names := engine.AuthorizedNamespaces(tt.bindings, ResourcePods, VerbList, tt.cluster)
			if all != tt.wantAll {
				t.Fatalf("all = %v, want %v", all, tt.wantAll)
			}
			if tt.wantAll {
				if names != nil {
					t.Fatalf("names should be nil when all=true, got %v", names)
				}
				return
			}
			if len(names) != len(tt.wantNames) {
				t.Fatalf("names = %v, want %v", keys(names), tt.wantNames)
			}
			for _, want := range tt.wantNames {
				if _, ok := names[want]; !ok {
					t.Fatalf("missing namespace %q in %v", want, keys(names))
				}
			}
		})
	}
}

func TestHasAnyNamespaceAccess(t *testing.T) {
	engine := NewEngine()
	clusterA := uuid.New()
	clusterB := uuid.New()

	tests := []struct {
		name     string
		bindings []RoleBinding
		cluster  uuid.UUID
		want     bool
	}{
		{"superuser", []RoleBinding{{IsSuperuser: true}}, clusterA, true},
		{"global reader", []RoleBinding{{RoleRules: readOnlyRules}}, clusterA, true},
		{"cluster-wide", []RoleBinding{{ClusterID: clusterA.String(), RoleRules: podListRule}}, clusterA, true},
		{"namespace-scoped grants any-access", []RoleBinding{{ClusterID: clusterA.String(), Namespace: "team-a", RoleRules: podListRule}}, clusterA, true},
		{"namespace-scoped on other cluster no access", []RoleBinding{{ClusterID: clusterB.String(), Namespace: "team-a", RoleRules: podListRule}}, clusterA, false},
		{"no grants", []RoleBinding{{ClusterID: clusterA.String(), Namespace: "team-a", RoleRules: []Rule{{Resource: "pods", Verbs: []string{"delete"}}}}}, clusterA, false},
		{"raw project binding no cluster access", []RoleBinding{{ProjectID: uuid.New().String(), RoleRules: podListRule}}, clusterA, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := engine.HasAnyNamespaceAccess(tt.bindings, ResourcePods, VerbList, tt.cluster); got != tt.want {
				t.Fatalf("HasAnyNamespaceAccess = %v, want %v", got, tt.want)
			}
		})
	}
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
