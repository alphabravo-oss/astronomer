package rbac

import (
	"testing"

	"github.com/google/uuid"
)

// Seed role rules from migration
var (
	adminRules = []Rule{{Resource: "*", Verbs: []string{"*"}}}

	standardUserRules = []Rule{
		{Resource: "clusters", Verbs: []string{"read", "list"}},
		{Resource: "projects", Verbs: []string{"read", "list"}},
		{Resource: "workloads", Verbs: []string{"read", "list"}},
		{Resource: "monitoring", Verbs: []string{"read", "list"}},
	}

	readOnlyRules = []Rule{{Resource: "*", Verbs: []string{"read", "list"}}}

	clusterOwnerRules = []Rule{{Resource: "*", Verbs: []string{"*"}}}

	clusterMemberRules = []Rule{
		{Resource: "clusters", Verbs: []string{"read"}},
		{Resource: "workloads", Verbs: []string{"read", "list", "create", "update", "delete", "scale", "restart"}},
		{Resource: "pods", Verbs: []string{"read", "list", "watch"}},
		{Resource: "monitoring", Verbs: []string{"read", "list"}},
	}

	projectViewerRules = []Rule{{Resource: "*", Verbs: []string{"read", "list", "watch"}}}
)

func TestCheckPermission(t *testing.T) {
	engine := NewEngine()

	clusterA := uuid.New()
	clusterB := uuid.New()
	projectA := uuid.New()
	projectB := uuid.New()
	nilUUID := uuid.Nil

	tests := []struct {
		name      string
		bindings  []RoleBinding
		resource  Resource
		verb      Verb
		clusterID uuid.UUID
		projectID uuid.UUID
		want      bool
	}{
		{
			name: "admin role grants everything",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: adminRules},
			},
			resource:  ResourceClusters,
			verb:      VerbDelete,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "admin role grants on any resource and verb",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: adminRules},
			},
			resource:  ResourceSecurity,
			verb:      VerbExec,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "standard user can read clusters",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: standardUserRules},
			},
			resource:  ResourceClusters,
			verb:      VerbRead,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "standard user can list projects",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: standardUserRules},
			},
			resource:  ResourceProjects,
			verb:      VerbList,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "standard user cannot delete clusters",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: standardUserRules},
			},
			resource:  ResourceClusters,
			verb:      VerbDelete,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      false,
		},
		{
			name: "standard user cannot access security",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: standardUserRules},
			},
			resource:  ResourceSecurity,
			verb:      VerbRead,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      false,
		},
		{
			name: "read only grants read on any resource",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: readOnlyRules},
			},
			resource:  ResourceBackups,
			verb:      VerbRead,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "read only grants list on any resource",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: readOnlyRules},
			},
			resource:  ResourceSecurity,
			verb:      VerbList,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "read only denies create",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: readOnlyRules},
			},
			resource:  ResourceClusters,
			verb:      VerbCreate,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      false,
		},
		{
			name: "cluster owner grants everything on specific cluster",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: clusterOwnerRules, ClusterID: clusterA.String()},
			},
			resource:  ResourceWorkloads,
			verb:      VerbDelete,
			clusterID: clusterA,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "cluster member can create workloads on specific cluster",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: clusterMemberRules, ClusterID: clusterA.String()},
			},
			resource:  ResourceWorkloads,
			verb:      VerbCreate,
			clusterID: clusterA,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "cluster member can scale workloads",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: clusterMemberRules, ClusterID: clusterA.String()},
			},
			resource:  ResourceWorkloads,
			verb:      VerbScale,
			clusterID: clusterA,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "cluster member can watch pods",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: clusterMemberRules, ClusterID: clusterA.String()},
			},
			resource:  ResourcePods,
			verb:      VerbWatch,
			clusterID: clusterA,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "project viewer grants read on specific project",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: projectViewerRules, ProjectID: projectA.String()},
			},
			resource:  ResourceWorkloads,
			verb:      VerbRead,
			clusterID: nilUUID,
			projectID: projectA,
			want:      true,
		},
		{
			name: "project viewer grants list on specific project",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: projectViewerRules, ProjectID: projectA.String()},
			},
			resource:  ResourcePods,
			verb:      VerbList,
			clusterID: nilUUID,
			projectID: projectA,
			want:      true,
		},
		{
			name: "no bindings denies everything",
			bindings: []RoleBinding{},
			resource:  ResourceClusters,
			verb:      VerbRead,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      false,
		},
		{
			name: "wrong cluster scope denies",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: clusterOwnerRules, ClusterID: clusterA.String()},
			},
			resource:  ResourceWorkloads,
			verb:      VerbDelete,
			clusterID: clusterB,
			projectID: nilUUID,
			want:      false,
		},
		{
			name: "wrong project scope denies",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: projectViewerRules, ProjectID: projectA.String()},
			},
			resource:  ResourceWorkloads,
			verb:      VerbRead,
			clusterID: nilUUID,
			projectID: projectB,
			want:      false,
		},
		{
			name: "wildcard resource with specific verb grants",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: readOnlyRules},
			},
			resource:  ResourceAlerts,
			verb:      VerbRead,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "specific resource with wildcard verb grants",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: []Rule{
					{Resource: "clusters", Verbs: []string{"*"}},
				}},
			},
			resource:  ResourceClusters,
			verb:      VerbDelete,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "global binding applies even when cluster is specified",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: adminRules},
			},
			resource:  ResourceWorkloads,
			verb:      VerbDelete,
			clusterID: clusterA,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "global binding applies even when project is specified",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: readOnlyRules},
			},
			resource:  ResourceWorkloads,
			verb:      VerbRead,
			clusterID: nilUUID,
			projectID: projectA,
			want:      true,
		},
		{
			name: "multiple bindings first match wins",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: standardUserRules},
				{UserID: "u1", RoleRules: adminRules},
			},
			resource:  ResourceSecurity,
			verb:      VerbDelete,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "cluster member cannot delete clusters",
			bindings: []RoleBinding{
				{UserID: "u1", RoleRules: clusterMemberRules, ClusterID: clusterA.String()},
			},
			resource:  ResourceClusters,
			verb:      VerbDelete,
			clusterID: clusterA,
			projectID: nilUUID,
			want:      false,
		},
		{
			name: "superuser bypass grants any resource/verb regardless of bindings",
			bindings: []RoleBinding{
				{UserID: "u1", IsSuperuser: true},
			},
			resource:  ResourceArgoCD,
			verb:      VerbSync,
			clusterID: clusterA,
			projectID: projectA,
			want:      true,
		},
		{
			name: "superuser bypass grants delete on rbac",
			bindings: []RoleBinding{
				{UserID: "u1", IsSuperuser: true},
			},
			resource:  ResourceRBAC,
			verb:      VerbDelete,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      true,
		},
		{
			name: "superuser flag overrides empty rules",
			bindings: []RoleBinding{
				{UserID: "u1", IsSuperuser: true, RoleRules: []Rule{{Resource: "clusters", Verbs: []string{"read"}}}},
			},
			resource:  ResourceClusters,
			verb:      VerbDelete,
			clusterID: nilUUID,
			projectID: nilUUID,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := engine.CheckPermission(tt.bindings, tt.resource, tt.verb, tt.clusterID, tt.projectID)
			if got != tt.want {
				t.Errorf("CheckPermission() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchRule(t *testing.T) {
	engine := NewEngine()

	tests := []struct {
		name     string
		rule     Rule
		resource Resource
		verb     Verb
		want     bool
	}{
		{
			name:     "wildcard resource and verb",
			rule:     Rule{Resource: "*", Verbs: []string{"*"}},
			resource: ResourceClusters,
			verb:     VerbDelete,
			want:     true,
		},
		{
			name:     "wildcard resource specific verb match",
			rule:     Rule{Resource: "*", Verbs: []string{"read", "list"}},
			resource: ResourceSecurity,
			verb:     VerbRead,
			want:     true,
		},
		{
			name:     "wildcard resource specific verb no match",
			rule:     Rule{Resource: "*", Verbs: []string{"read", "list"}},
			resource: ResourceSecurity,
			verb:     VerbDelete,
			want:     false,
		},
		{
			name:     "specific resource wildcard verb",
			rule:     Rule{Resource: "clusters", Verbs: []string{"*"}},
			resource: ResourceClusters,
			verb:     VerbExec,
			want:     true,
		},
		{
			name:     "specific resource specific verb match",
			rule:     Rule{Resource: "clusters", Verbs: []string{"read", "list"}},
			resource: ResourceClusters,
			verb:     VerbList,
			want:     true,
		},
		{
			name:     "specific resource specific verb no match",
			rule:     Rule{Resource: "clusters", Verbs: []string{"read", "list"}},
			resource: ResourceClusters,
			verb:     VerbDelete,
			want:     false,
		},
		{
			name:     "wrong resource",
			rule:     Rule{Resource: "clusters", Verbs: []string{"*"}},
			resource: ResourceProjects,
			verb:     VerbRead,
			want:     false,
		},
		{
			name:     "empty verbs list",
			rule:     Rule{Resource: "*", Verbs: []string{}},
			resource: ResourceClusters,
			verb:     VerbRead,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := engine.matchRule(tt.rule, tt.resource, tt.verb)
			if got != tt.want {
				t.Errorf("matchRule() = %v, want %v", got, tt.want)
			}
		})
	}
}
