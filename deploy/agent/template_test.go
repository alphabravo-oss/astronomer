package agenttemplate

import (
	"strings"
	"testing"
)

func TestRBACRulesYAMLProfiles(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		want    []string
		notWant []string
	}{
		{
			name:    "admin",
			profile: PrivilegeProfileAdmin,
			want: []string{
				`apiGroups: ["*"]`,
				`resources: ["*"]`,
				`verbs: ["*"]`,
				`nonResourceURLs: ["*"]`,
			},
		},
		{
			name:    "viewer",
			profile: PrivilegeProfileViewer,
			want: []string{
				`resources: ["configmaps", "endpoints", "events", "namespaces", "nodes"`,
				`resources: ["customresourcedefinitions"]`,
				`verbs: ["get", "list", "watch"]`,
			},
			notWant: []string{
				`resources: ["*"]`,
				`verbs: ["*"]`,
				`"create"`,
				`pods/exec`,
			},
		},
		{
			name:    "operator",
			profile: PrivilegeProfileOperator,
			want: []string{
				`pods/exec`,
				`resources: ["roles", "rolebindings"]`,
				`verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]`,
			},
			notWant: []string{
				`resources: ["*"]`,
				`verbs: ["*"]`,
				`clusterroles`,
				`clusterrolebindings`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RBACRulesYAML(tt.profile)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("profile %s missing %q:\n%s", tt.profile, want, got)
				}
			}
			for _, unwanted := range tt.notWant {
				if strings.Contains(got, unwanted) {
					t.Fatalf("profile %s unexpectedly contains %q:\n%s", tt.profile, unwanted, got)
				}
			}
		})
	}
}

func TestRenderInstallYAMLUsesPrivilegeProfile(t *testing.T) {
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:         "https://astro.example.com",
		ClusterID:         "c1",
		RegistrationToken: "token",
		AgentImage:        "example.com/agent:v1",
		PrivilegeProfile:  PrivilegeProfileViewer,
	})
	for _, want := range []string{
		`SERVER_URL: "https://astro.example.com"`,
		`image: "example.com/agent:v1"`,
		`verbs: ["get", "list", "watch"]`,
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing %q", want)
		}
	}
	for _, unwanted := range []string{`{{AGENT_RBAC_RULES}}`, `resources: ["*"]`, `verbs: ["*"]`} {
		if strings.Contains(manifest, unwanted) {
			t.Fatalf("manifest unexpectedly contains %q", unwanted)
		}
	}
}
