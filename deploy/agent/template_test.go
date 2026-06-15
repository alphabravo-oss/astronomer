package agenttemplate

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
				// RBAC objects are present but read-only (no self-escalation).
				`resources: ["clusterroles", "clusterrolebindings", "roles", "rolebindings"]`,
				// Workload mutation verbs remain for day-2 ops.
				`verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]`,
			},
			notWant: []string{
				`resources: ["*"]`,
				`verbs: ["*"]`,
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
				// Namespace operator must never reach cluster-scoped RBAC.
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

// TestOperatorProfilesGrantNoRBACWriteVerbs is the negative guard for H6: the
// operator and namespace-operator profiles must never carry write verbs on
// rbac.authorization.k8s.io resources (roles/rolebindings/clusterroles/
// clusterrolebindings). Write access there is a self-escalation primitive and
// belongs exclusively to the explicit admin profile.
func TestOperatorProfilesGrantNoRBACWriteVerbs(t *testing.T) {
	type rbacRule struct {
		APIGroups []string `yaml:"apiGroups"`
		Resources []string `yaml:"resources"`
		Verbs     []string `yaml:"verbs"`
	}

	writeVerbs := map[string]bool{
		"create": true, "update": true, "patch": true, "delete": true,
		"deletecollection": true, "*": true,
	}

	for _, profile := range []string{PrivilegeProfileOperator, PrivilegeProfileNamespaceOperator} {
		t.Run(profile, func(t *testing.T) {
			var rules []rbacRule
			if err := yaml.Unmarshal([]byte(RBACRulesYAML(profile)), &rules); err != nil {
				t.Fatalf("parse rules for %q: %v", profile, err)
			}
			sawRBAC := false
			for _, r := range rules {
				isRBAC := false
				for _, g := range r.APIGroups {
					if g == "rbac.authorization.k8s.io" || g == "*" {
						isRBAC = true
					}
				}
				if !isRBAC {
					continue
				}
				sawRBAC = true
				for _, v := range r.Verbs {
					if writeVerbs[v] {
						t.Fatalf("profile %q grants write verb %q on RBAC resources %v (self-escalation primitive)", profile, v, r.Resources)
					}
				}
			}
			if !sawRBAC {
				t.Fatalf("profile %q has no rbac.authorization.k8s.io rule; expected read-only RBAC access", profile)
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

// TestNormalizePrivilegeProfileDefaultsToAdmin: an unspecified (empty/blank)
// profile defaults to full management control (admin), matching Rancher's
// cluster-admin agent model. The per-user gate is the management-plane RBAC.
func TestNormalizePrivilegeProfileDefaultsToAdmin(t *testing.T) {
	for _, input := range []string{"", "   ", "\t"} {
		if got := NormalizePrivilegeProfile(input); got != PrivilegeProfileAdmin {
			t.Fatalf("NormalizePrivilegeProfile(%q) = %q, want %q (unspecified -> admin)", input, got, PrivilegeProfileAdmin)
		}
	}
}

// TestNormalizePrivilegeProfileUnknownFailsClosed: an explicitly-supplied but
// UNRECOGNIZED profile (a typo/misconfig) must fail closed to viewer rather
// than silently granting admin.
func TestNormalizePrivilegeProfileUnknownFailsClosed(t *testing.T) {
	for _, input := range []string{"garbage", "cluster-admin", "root", "superuser", "unknown-profile"} {
		if got := NormalizePrivilegeProfile(input); got != PrivilegeProfileViewer {
			t.Fatalf("NormalizePrivilegeProfile(%q) = %q, want %q (unknown -> fail closed)", input, got, PrivilegeProfileViewer)
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

// TestRenderInstallYAMLDefaultProfileResolvesToAdmin: an agent manifest
// rendered with no explicit profile defaults to full management control
// (admin), matching Rancher's cluster-admin agent model. The per-user security
// boundary is the management-plane RBAC.
func TestRenderInstallYAMLDefaultProfileResolvesToAdmin(t *testing.T) {
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:         "https://astro.example.com",
		ClusterID:         "c1",
		RegistrationToken: "token",
		AgentImage:        "example.com/agent:v1",
		// PrivilegeProfile intentionally left empty.
	})
	if !strings.Contains(manifest, `PRIVILEGE_PROFILE: "admin"`) {
		t.Fatalf("default manifest should resolve to admin profile:\n%s", manifest)
	}
	for _, wanted := range []string{
		`resources: ["*"]`,
		`verbs: ["*"]`,
		`nonResourceURLs: ["*"]`,
	} {
		if !strings.Contains(manifest, wanted) {
			t.Fatalf("default (admin) manifest should contain full-access rule %q:\n%s", wanted, manifest)
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

// TestRenderInstallYAMLIsValidYAMLForEveryProfile is the regression guard for
// the bug where the multi-line {{AGENT_RBAC_RULES}} value (notably the admin
// profile's */*/* rules) was string-replaced INTO the header-comment block
// that documented the placeholders, breaking the rendered YAML. Every profile
// must render a manifest whose documents all parse as valid YAML.
func TestRenderInstallYAMLIsValidYAMLForEveryProfile(t *testing.T) {
	profiles := []string{
		PrivilegeProfileAdmin,
		PrivilegeProfileViewer,
		PrivilegeProfileOperator,
		PrivilegeProfileNamespaceViewer,
		PrivilegeProfileNamespaceOperator,
		PrivilegeProfileCustom,
		"", // unspecified -> admin default
	}
	for _, p := range profiles {
		manifest := RenderInstallYAML(InstallTemplateData{
			ServerURL:         "https://astro.example.com",
			ClusterID:         "c1",
			RegistrationToken: "tok",
			AgentImage:        "example.com/agent:v1",
			PrivilegeProfile:  p,
		})
		dec := yaml.NewDecoder(strings.NewReader(manifest))
		docs := 0
		for {
			var doc any
			err := dec.Decode(&doc)
			if err != nil {
				if err.Error() == "EOF" {
					break
				}
				t.Fatalf("profile %q rendered invalid YAML at doc %d: %v\n%s", p, docs, err, manifest)
			}
			docs++
		}
		if docs < 5 {
			t.Fatalf("profile %q rendered only %d YAML docs, expected the full agent manifest", p, docs)
		}
	}
}
