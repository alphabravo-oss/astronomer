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
				`"secrets"`,
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
		{
			name:    "namespace viewer",
			profile: PrivilegeProfileNamespaceViewer,
			want: []string{
				`Namespace-scoped read-only inventory`,
				`resources: ["configmaps", "endpoints", "events", "persistentvolumeclaims"`,
				`verbs: ["get", "list", "watch"]`,
			},
			notWant: []string{
				`resources: ["namespaces", "nodes"`,
				`resources: ["customresourcedefinitions"]`,
				`nonResourceURLs`,
				`"secrets"`,
				`pods/exec`,
			},
		},
		{
			name:    "namespace operator",
			profile: PrivilegeProfileNamespaceOperator,
			want: []string{
				`Namespace-scoped workload operations`,
				`pods/exec`,
				`resources: ["roles", "rolebindings"]`,
			},
			notWant: []string{
				`resources: ["namespaces", "nodes"`,
				`resources: ["customresourcedefinitions"]`,
				`nonResourceURLs`,
				`clusterroles`,
				`clusterrolebindings`,
				`"secrets"`,
			},
		},
		{
			name:    "custom",
			profile: PrivilegeProfileCustom,
			want: []string{
				`No default Kubernetes permissions`,
				`[]`,
			},
			notWant: []string{
				`resources: ["*"]`,
				`verbs: ["*"]`,
				`verbs: ["get", "list", "watch"]`,
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

func TestRBACBindingKindProfiles(t *testing.T) {
	tests := []struct {
		profile       string
		wantKind      string
		wantNamespace bool
	}{
		{PrivilegeProfileViewer, "ClusterRoleBinding", false},
		{PrivilegeProfileOperator, "ClusterRoleBinding", false},
		{PrivilegeProfileAdmin, "ClusterRoleBinding", false},
		{PrivilegeProfileCustom, "ClusterRoleBinding", false},
		{PrivilegeProfileNamespaceViewer, "RoleBinding", true},
		{PrivilegeProfileNamespaceOperator, "RoleBinding", true},
	}
	for _, tt := range tests {
		if got := RBACBindingKind(tt.profile); got != tt.wantKind {
			t.Fatalf("RBACBindingKind(%q) = %q, want %q", tt.profile, got, tt.wantKind)
		}
		hasNamespace := strings.Contains(RBACBindingNamespaceLine(tt.profile), "namespace: astronomer-system")
		if hasNamespace != tt.wantNamespace {
			t.Fatalf("RBACBindingNamespaceLine(%q) namespace=%v, want %v", tt.profile, hasNamespace, tt.wantNamespace)
		}
	}
}

func TestNormalizePrivilegeProfileAcceptsNamespaceAliasesAndCustom(t *testing.T) {
	tests := map[string]string{
		"namespace_viewer":    PrivilegeProfileNamespaceViewer,
		"namespaced-viewer":   PrivilegeProfileNamespaceViewer,
		"namespace operator":  PrivilegeProfileNamespaceOperator,
		"namespaced_operator": PrivilegeProfileNamespaceOperator,
		"custom":              PrivilegeProfileCustom,
	}
	for input, want := range tests {
		if got := NormalizePrivilegeProfile(input); got != want {
			t.Fatalf("NormalizePrivilegeProfile(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestNormalizePrivilegeProfileFailsClosedToViewer is the negative test for
// finding C2: an empty or unrecognized profile must resolve to the read-only
// viewer profile, never the cluster-admin "admin" profile.
func TestNormalizePrivilegeProfileFailsClosedToViewer(t *testing.T) {
	for _, input := range []string{"", "   ", "garbage", "cluster-admin", "root", "superuser", "unknown-profile"} {
		if got := NormalizePrivilegeProfile(input); got != PrivilegeProfileViewer {
			t.Fatalf("NormalizePrivilegeProfile(%q) = %q, want %q (must fail closed to viewer)", input, got, PrivilegeProfileViewer)
		}
		if got := NormalizePrivilegeProfile(input); got == PrivilegeProfileAdmin {
			t.Fatalf("NormalizePrivilegeProfile(%q) must not silently default to admin", input)
		}
	}
}

// TestNormalizePrivilegeProfileExplicitProfilesStillResolve proves that
// deliberately chosen broader profiles continue to work after the C2 fix.
func TestNormalizePrivilegeProfileExplicitProfilesStillResolve(t *testing.T) {
	tests := map[string]string{
		"admin":              PrivilegeProfileAdmin,
		"ADMIN":              PrivilegeProfileAdmin,
		"  admin  ":          PrivilegeProfileAdmin,
		"operator":           PrivilegeProfileOperator,
		"Operator":           PrivilegeProfileOperator,
		"viewer":             PrivilegeProfileViewer,
		"namespace-operator": PrivilegeProfileNamespaceOperator,
		"namespace-viewer":   PrivilegeProfileNamespaceViewer,
	}
	for input, want := range tests {
		if got := NormalizePrivilegeProfile(input); got != want {
			t.Fatalf("NormalizePrivilegeProfile(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestRenderInstallYAMLDefaultProfileHasNoFullAccessRule is the renderer-level
// negative test for C2: an agent manifest rendered with no explicit profile
// must not contain the cluster-admin */*/* wildcard rule.
func TestRenderInstallYAMLDefaultProfileHasNoFullAccessRule(t *testing.T) {
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:         "https://astro.example.com",
		ClusterID:         "c1",
		RegistrationToken: "token",
		AgentImage:        "example.com/agent:v1",
		// PrivilegeProfile intentionally left empty.
	})
	if !strings.Contains(manifest, `PRIVILEGE_PROFILE: "viewer"`) {
		t.Fatalf("default manifest should resolve to viewer profile:\n%s", manifest)
	}
	for _, unwanted := range []string{
		`resources: ["*"]`,
		`verbs: ["*"]`,
		`nonResourceURLs: ["*"]`,
	} {
		if strings.Contains(manifest, unwanted) {
			t.Fatalf("default manifest unexpectedly contains cluster-admin rule %q:\n%s", unwanted, manifest)
		}
	}
}

// TestRenderInstallYAMLExplicitAdminStillRendersFullAccess proves that
// explicitly choosing admin still renders the full-access RBAC rules.
func TestRenderInstallYAMLExplicitAdminStillRendersFullAccess(t *testing.T) {
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:         "https://astro.example.com",
		ClusterID:         "c1",
		RegistrationToken: "token",
		AgentImage:        "example.com/agent:v1",
		PrivilegeProfile:  PrivilegeProfileAdmin,
	})
	for _, want := range []string{
		`PRIVILEGE_PROFILE: "admin"`,
		`resources: ["*"]`,
		`verbs: ["*"]`,
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("explicit admin manifest missing %q:\n%s", want, manifest)
		}
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
		`PRIVILEGE_PROFILE: "viewer"`,
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

func TestRenderInstallYAMLUsesNamespacedRoleBinding(t *testing.T) {
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:         "https://astro.example.com",
		ClusterID:         "c1",
		RegistrationToken: "token",
		AgentImage:        "example.com/agent:v1",
		PrivilegeProfile:  PrivilegeProfileNamespaceOperator,
	})
	for _, want := range []string{
		`PRIVILEGE_PROFILE: "namespace-operator"`,
		`kind: RoleBinding`,
		`namespace: astronomer-system`,
		`Namespace-scoped workload operations`,
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing %q:\n%s", want, manifest)
		}
	}
	for _, unwanted := range []string{`kind: ClusterRoleBinding`, `resources: ["*"]`, `verbs: ["*"]`, `"secrets"`} {
		if strings.Contains(manifest, unwanted) {
			t.Fatalf("manifest unexpectedly contains %q:\n%s", unwanted, manifest)
		}
	}
}

func TestRenderInstallYAMLUsesInstallMetadata(t *testing.T) {
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:          "https://astro.example.com",
		ClusterID:          "c1",
		RegistrationToken:  "token",
		AgentImage:         "example.com/agent:v1",
		PrivilegeProfile:   PrivilegeProfileOperator,
		ServiceAccountName: "team-agent",
		PodLabels: map[string]string{
			"team":             "platform",
			"example.com/tier": `gold"primary`,
		},
	})
	for _, want := range []string{
		"name: team-agent",
		"serviceAccountName: team-agent",
		`team: "platform"`,
		`example.com/tier: "gold\"primary"`,
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing %q:\n%s", want, manifest)
		}
	}
	for _, unwanted := range []string{"{{AGENT_SERVICE_ACCOUNT_NAME}}", "{{AGENT_POD_LABELS}}"} {
		if strings.Contains(manifest, unwanted) {
			t.Fatalf("manifest still contains placeholder %q:\n%s", unwanted, manifest)
		}
	}
}
