package argolabels

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestSanitizeLabelKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"simple lower", "team", "team"},
		{"uppercase folded", "Team", "team"},
		{"space becomes dash", "Team Name", "team-name"},
		{"multiple spaces collapse", "Team   Name", "team-name"},
		{"slash becomes dash", "team/name", "team-name"},
		{"underscores become dash", "team_name", "team-name"},
		{"leading non-alnum stripped", "_team", "team"},
		{"trailing dash stripped", "team-", "team"},
		{"dots preserved", "team.name", "team.name"},
		{"truncation at 63", strings.Repeat("a", 80), strings.Repeat("a", 63)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeLabelKey(tc.in); got != tc.want {
				t.Fatalf("SanitizeLabelKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestManagedClusterLabelsWithSingleProject(t *testing.T) {
	clusterID := uuid.New()
	projectID := uuid.New()
	labels := ManagedClusterLabels(sqlc.Cluster{
		ID:                clusterID,
		Name:              "prod-east",
		Environment:       "production",
		AgentVersion:      "v0.4.1",
		KubernetesVersion: "v1.29.3+k3s1",
		Labels:            json.RawMessage(`{"Team Name":"platform"}`),
	}, []sqlc.Project{{ID: projectID, Name: "Platform Team"}})

	want := map[string]string{
		ManagedByLabelKey:                              ManagedByLabelValue,
		ClusterIDLabelKey:                              clusterID.String(),
		ClusterNameLabelKey:                            "prod-east",
		EnvironmentLabelKey:                            "production",
		IsLocalLabelKey:                                "false",
		// No explicit privilege-profile annotation -> full management control
		// (default; matches Rancher's cluster-admin agent model).
		AgentProfileLabelKey:                           "admin",
		AgentVersionLabelKey:                           "v0.4.1",
		KubernetesVersionLabelKey:                      "v1.29.3-k3s1",
		LabelPrefix + "team-name":                      "platform",
		ProjectIDLabelKey:                              projectID.String(),
		ProjectLabelKey:                                "platform-team",
		ProjectIDMembershipPrefix + projectID.String(): ProjectMembershipLabelValue,
		ProjectMembershipPrefix + "platform-team":      ProjectMembershipLabelValue,
	}
	for key, value := range want {
		if got := labels[key]; got != value {
			t.Fatalf("labels[%q] = %q, want %q (full=%v)", key, got, value, labels)
		}
	}
}

func TestManagedClusterLabelsWithMultipleProjects(t *testing.T) {
	projectA := uuid.New()
	projectB := uuid.New()
	labels := ManagedClusterLabels(sqlc.Cluster{ID: uuid.New(), Name: "prod-east"}, []sqlc.Project{
		{ID: projectA, Name: "Platform"},
		{ID: projectB, Name: "Data Science"},
	})

	if _, ok := labels[ProjectIDLabelKey]; ok {
		t.Fatalf("single project id label should be omitted for multi-project clusters: %v", labels)
	}
	if _, ok := labels[ProjectLabelKey]; ok {
		t.Fatalf("single project label should be omitted for multi-project clusters: %v", labels)
	}
	want := map[string]string{
		ProjectIDMembershipPrefix + projectA.String(): ProjectMembershipLabelValue,
		ProjectIDMembershipPrefix + projectB.String(): ProjectMembershipLabelValue,
		ProjectMembershipPrefix + "platform":          ProjectMembershipLabelValue,
		ProjectMembershipPrefix + "data-science":      ProjectMembershipLabelValue,
	}
	for key, value := range want {
		if got := labels[key]; got != value {
			t.Fatalf("labels[%q] = %q, want %q (full=%v)", key, got, value, labels)
		}
	}
}

func TestProjectsForCluster(t *testing.T) {
	clusterID := uuid.New()
	projectID := uuid.New()
	fake := &fakeProjectLister{projects: []sqlc.Project{{ID: projectID, Name: "Platform"}}}

	got, err := ProjectsForCluster(context.Background(), fake, clusterID)
	if err != nil {
		t.Fatalf("ProjectsForCluster returned error: %v", err)
	}
	if len(got) != 1 || got[0].ID != projectID {
		t.Fatalf("ProjectsForCluster = %#v", got)
	}
	if fake.arg.ClusterID != clusterID || fake.arg.Limit != 1000 || fake.arg.Offset != 0 {
		t.Fatalf("ListProjectsByCluster params = %#v", fake.arg)
	}

	got, err = ProjectsForCluster(context.Background(), struct{}{}, clusterID)
	if err != nil {
		t.Fatalf("ProjectsForCluster unsupported query returned error: %v", err)
	}
	if got != nil {
		t.Fatalf("ProjectsForCluster unsupported query = %#v, want nil", got)
	}
}

type fakeProjectLister struct {
	arg      sqlc.ListProjectsByClusterParams
	projects []sqlc.Project
}

func (f *fakeProjectLister) ListProjectsByCluster(_ context.Context, arg sqlc.ListProjectsByClusterParams) ([]sqlc.Project, error) {
	f.arg = arg
	return f.projects, nil
}

// TestClusterAgentPrivilegeProfileDefaultsToAdmin: at the annotation-read
// layer, an UNSPECIFIED profile (no annotations / unparseable / no profile key)
// defaults to full management control (admin), matching Rancher's cluster-admin
// agent model — the per-user gate is the management-plane RBAC.
func TestClusterAgentPrivilegeProfileDefaultsToAdmin(t *testing.T) {
	cases := map[string]json.RawMessage{
		"empty":                 nil,
		"unparseable":           json.RawMessage(`not-json`),
		"no profile annotation": json.RawMessage(`{"some/other":"value"}`),
	}
	for name, raw := range cases {
		if got := ClusterAgentPrivilegeProfile(raw); got != agenttemplate.PrivilegeProfileAdmin {
			t.Fatalf("ClusterAgentPrivilegeProfile(%s) = %q, want %q", name, got, agenttemplate.PrivilegeProfileAdmin)
		}
	}
}

// TestClusterAgentPrivilegeProfileUnknownFailsClosed: an explicit but
// UNRECOGNIZED profile string (typo/misconfig) must fail closed to viewer, not
// silently grant admin.
func TestClusterAgentPrivilegeProfileUnknownFailsClosed(t *testing.T) {
	raw := json.RawMessage(`{"astronomer.io/agent-privilege-profile":"cluster-admin"}`)
	if got := ClusterAgentPrivilegeProfile(raw); got != agenttemplate.PrivilegeProfileViewer {
		t.Fatalf("ClusterAgentPrivilegeProfile(unknown) = %q, want %q", got, agenttemplate.PrivilegeProfileViewer)
	}
}

// TestClusterAgentPrivilegeProfileExplicitProfilesPreserved proves that
// explicitly annotated profiles still resolve after the C2 fix.
func TestClusterAgentPrivilegeProfileExplicitProfilesPreserved(t *testing.T) {
	cases := map[string]string{
		"admin":    agenttemplate.PrivilegeProfileAdmin,
		"operator": agenttemplate.PrivilegeProfileOperator,
		"viewer":   agenttemplate.PrivilegeProfileViewer,
	}
	for annotation, want := range cases {
		raw := json.RawMessage(`{"astronomer.io/agent-privilege-profile":"` + annotation + `"}`)
		if got := ClusterAgentPrivilegeProfile(raw); got != want {
			t.Fatalf("ClusterAgentPrivilegeProfile(%q) = %q, want %q", annotation, got, want)
		}
	}
}
