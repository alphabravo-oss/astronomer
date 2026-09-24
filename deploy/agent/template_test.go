package agenttemplate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
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
				`resources: ["configmaps", "endpoints", "events", "limitranges", "namespaces", "nodes"`,
				`"resourcequotas"`,
				`resources: ["customresourcedefinitions"]`,
				// Inventory mirrors the agent watches read-only.
				`apiGroups: ["events.k8s.io"]`,
				`resources: ["ingresses", "ingressclasses", "networkpolicies"]`,
				`resources: ["gatewayclasses", "gateways", "httproutes", "grpcroutes", "referencegrants", "tcproutes", "udproutes", "tlsroutes"]`,
				`resources: ["vulnerabilityreports"]`,
				`verbs: ["get", "list", "watch"]`,
			},
			notWant: []string{
				`resources: ["*"]`,
				`verbs: ["*"]`,
				`"create"`,
				`pods/exec`,
				// Viewer must never read secret data.
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
				// Cluster-wide READ is deliberate and required for complete inventory,
				// including CRDs installed after adoption. An enumerated allowlist can
				// never remain complete. This wildcard is read-only; wildcard
				// verbs stay forbidden below, so operator is still not cluster-admin.
				// See TestInventoryProfilesGrantClusterWideRead.
				`resources: ["*"]`,
			},
			notWant: []string{
				// operator must never be cluster-admin: no wildcard WRITE. The
				// read-only wildcard above is the only wildcard permitted here.
				`verbs: ["*"]`,
			},
		},
		{
			name:    "namespace viewer",
			profile: PrivilegeProfileNamespaceViewer,
			want: []string{
				`Namespace-scoped read-only inventory`,
				`resources: ["configmaps", "endpoints", "events", "persistentvolumeclaims"`,
				`resources: ["gateways", "httproutes", "grpcroutes", "referencegrants", "tcproutes", "udproutes", "tlsroutes"]`,
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

// TestOperatorProfileIsPrivilegedNearAdmin (H4) is the HONEST counter-assertion
// to the test above. The "no RBAC-write" check only rules out DIRECT
// self-escalation; it must NOT be read as containment, because operator grants
// cluster-wide secrets read+write and pod exec/attach/portforward — textbook
// INDIRECT cluster-admin primitives. This test pins that reality so any future
// change that genuinely contains `operator` (trims these grants) is forced to
// consciously update it, rather than silently re-introducing the false contract.
func TestOperatorProfileIsPrivilegedNearAdmin(t *testing.T) {
	type rbacRule struct {
		APIGroups []string `yaml:"apiGroups"`
		Resources []string `yaml:"resources"`
		Verbs     []string `yaml:"verbs"`
	}
	var rules []rbacRule
	if err := yaml.Unmarshal([]byte(RBACRulesYAML(PrivilegeProfileOperator)), &rules); err != nil {
		t.Fatalf("parse operator rules: %v", err)
	}
	has := func(res, verb string) bool {
		for _, r := range rules {
			coreGroup := false
			for _, g := range r.APIGroups {
				if g == "" || g == "*" {
					coreGroup = true
				}
			}
			if !coreGroup {
				continue
			}
			resOK, verbOK := false, false
			for _, x := range r.Resources {
				if x == res || x == "*" {
					resOK = true
				}
			}
			for _, v := range r.Verbs {
				if v == verb || v == "*" {
					verbOK = true
				}
			}
			if resOK && verbOK {
				return true
			}
		}
		return false
	}

	// Indirect cluster-admin primitives that make `operator` near-admin. If a
	// future change removes any of these (containing operator), update this test
	// AND the honest-scope comment/docs together — do not silently drop the
	// assertion and let the "no direct escalation" test imply containment again.
	if !has("secrets", "get") {
		t.Error("operator no longer grants cluster-wide secret READ — if intentional (containment), update the H4 honest-scope comment + docs")
	}
	if !has("secrets", "create") && !has("secrets", "update") && !has("secrets", "patch") {
		t.Error("operator no longer grants cluster-wide secret WRITE — if intentional, update the H4 honest-scope comment + docs")
	}
	if !has("pods/exec", "create") {
		t.Error("operator no longer grants pods/exec — if intentional (containment), update the H4 honest-scope comment + docs")
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

// TestNormalizePrivilegeProfileDefaultsToViewer: an unspecified (empty/blank)
// profile defaults to least-privilege read-only viewer. Broadening to
// operator/admin is an explicit opt-in chosen at registration, so a
// no-annotation adoption is safe by default.
func TestNormalizePrivilegeProfileDefaultsToViewer(t *testing.T) {
	for _, input := range []string{"", "   ", "\t"} {
		if got := NormalizePrivilegeProfile(input); got != PrivilegeProfileViewer {
			t.Fatalf("NormalizePrivilegeProfile(%q) = %q, want %q (unspecified -> viewer)", input, got, PrivilegeProfileViewer)
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

// TestRenderInstallYAMLEscapesScalars (L7) proves a malicious operator-controlled
// scalar (for example the configured release image) cannot break
// out of its double-quoted YAML scalar and inject arbitrary manifest content:
// the rendered manifest still parses as valid multi-doc YAML and the injection
// payload does not appear as a structural element.
func TestRenderInstallYAMLEscapesScalars(t *testing.T) {
	// A payload that, unescaped, would close the image scalar and inject a key.
	evil := `x:v1"
  evilInjected: "pwned`
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:         `https://h" injected: "1`,
		ClusterID:         "c1",
		RegistrationToken: "token",
		AgentImage:        evil,
		PrivilegeProfile:  "viewer",
	})
	// Every document must still parse — an injection would yield a YAML error or
	// an unexpected top-level/structural key.
	dec := yaml.NewDecoder(strings.NewReader(manifest))
	docs := 0
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("rendered manifest is not valid YAML (injection broke it): %v", err)
		}
		docs++
		if _, leaked := doc["evilInjected"]; leaked {
			t.Fatalf("injection payload leaked as a top-level key:\n%s", manifest)
		}
	}
	if docs == 0 {
		t.Fatal("no documents parsed")
	}
	if strings.Contains(manifest, "evilInjected: \"pwned") && !strings.Contains(manifest, `\"`) {
		t.Fatalf("payload was not escaped:\n%s", manifest)
	}
}

// TestEveryOwnedNamespaceHasExplicitPSA (L6) asserts every Astronomer-owned
// namespace carries an explicit pod-security.kubernetes.io/enforce label, so its
// PSA posture never silently inherits an unpredictable cluster default (the
// "real bite" being fluent-bit's hostPath in astronomer-logging).
func TestEveryOwnedNamespaceHasExplicitPSA(t *testing.T) {
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL: "https://h", ClusterID: "c1", RegistrationToken: "t", AgentImage: "i:v1", PrivilegeProfile: "viewer",
	})
	type nsDoc struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name   string            `yaml:"name"`
			Labels map[string]string `yaml:"labels"`
		} `yaml:"metadata"`
	}
	dec := yaml.NewDecoder(strings.NewReader(manifest))
	seen := map[string]bool{}
	for {
		var d nsDoc
		if err := dec.Decode(&d); err != nil {
			break
		}
		if d.Kind != "Namespace" {
			continue
		}
		seen[d.Metadata.Name] = true
		if d.Metadata.Labels["pod-security.kubernetes.io/enforce"] == "" {
			t.Errorf("owned namespace %q has no explicit pod-security.kubernetes.io/enforce label (L6)", d.Metadata.Name)
		}
	}
	for _, want := range AstronomerOwnedNamespaces {
		if !seen[want] {
			t.Errorf("expected owned namespace %q in the rendered manifest", want)
		}
	}
}

func TestRenderInstallYAMLPlaintextDevelopmentURLAcknowledgesAgentInsecureMode(t *testing.T) {
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:         "http://host.k3d.internal:8080",
		ClusterID:         "c1",
		RegistrationToken: "token",
		AgentImage:        "example.com/agent:v1",
	})
	if !strings.Contains(manifest, `INSECURE: "true"`) {
		t.Fatalf("plaintext development manifest did not acknowledge agent insecure mode:\n%s", manifest)
	}
}

func TestRenderInstallYAMLSplitsBootstrapAndDurableCredentialOwnership(t *testing.T) {
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:         "https://astro.example.com",
		ClusterID:         "c1",
		RegistrationToken: "registration-material",
		AgentImage:        "example.com/agent:v1",
		PrivilegeProfile:  PrivilegeProfileViewer,
	})
	if strings.Contains(manifest, "kubectl.kubernetes.io/last-applied-configuration") {
		t.Fatal("rendered manifest must not contain a client-side last-applied annotation")
	}
	if strings.Contains(manifest, "ASTRONOMER_AGENT_TOKEN") {
		t.Fatal("current in-cluster manifest must resolve credentials through exact-name API reads, not token env")
	}

	type rule struct {
		Resources     []string `yaml:"resources"`
		ResourceNames []string `yaml:"resourceNames"`
		Verbs         []string `yaml:"verbs"`
	}
	type doc struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name   string            `yaml:"name"`
			Labels map[string]string `yaml:"labels"`
		} `yaml:"metadata"`
		StringData map[string]string `yaml:"stringData"`
		Data       map[string]string `yaml:"data"`
		Rules      []rule            `yaml:"rules"`
	}
	var identityRole []rule
	credentialRBACObjects := map[string]bool{}
	secretNames := map[string]bool{}
	dec := yaml.NewDecoder(strings.NewReader(manifest))
	for {
		var d doc
		if err := dec.Decode(&d); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatal(err)
		}
		if d.Kind == "Secret" {
			secretNames[d.Metadata.Name] = true
			if d.Metadata.Name == "astronomer-agent-registration-token" && d.StringData["token"] != "registration-material" {
				t.Fatal("bootstrap Secret does not contain rendered registration material")
			}
			if d.Metadata.Name == "astronomer-agent-identity" {
				if len(d.StringData) != 0 || len(d.Data) != 0 {
					t.Fatal("installer identity container must not contain token data")
				}
				if d.Metadata.Labels["astronomer.io/agent-credential-purpose"] != "durable-identity-container" {
					t.Fatal("identity container purpose label is missing")
				}
			}
		}
		if (d.Kind == "Role" || d.Kind == "RoleBinding") && (d.Metadata.Name == "astronomer-agent-identity" || d.Metadata.Name == "astronomer-agent-token") {
			credentialRBACObjects[d.Kind+"/"+d.Metadata.Name] = true
		}
		if d.Kind == "Role" && d.Metadata.Name == "astronomer-agent-identity" {
			identityRole = d.Rules
		}
	}
	if !secretNames["astronomer-agent-registration-token"] {
		t.Fatal("installer-owned bootstrap Secret is missing")
	}
	if !secretNames["astronomer-agent-identity"] {
		t.Fatal("installer-owned empty identity container is missing")
	}
	if secretNames["astronomer-agent-token"] {
		t.Fatal("legacy durable Secret must be absent from current installer manifest")
	}
	if len(identityRole) != 3 {
		t.Fatalf("credential Role has %d rules, want exact-name get, patch, and delete", len(identityRole))
	}
	if !credentialRBACObjects["Role/astronomer-agent-identity"] || !credentialRBACObjects["RoleBinding/astronomer-agent-identity"] {
		t.Fatal("current credential Role/Binding is missing")
	}
	if credentialRBACObjects["Role/astronomer-agent-token"] || credentialRBACObjects["RoleBinding/astronomer-agent-token"] {
		t.Fatal("current manifest must not own the cached-manifest legacy credential Role/Binding names")
	}

	allows := func(verb, name string) bool {
		for _, r := range identityRole {
			if !containsString(r.Resources, "secrets") || !containsString(r.Verbs, verb) {
				continue
			}
			if len(r.ResourceNames) == 0 || containsString(r.ResourceNames, name) {
				return true
			}
		}
		return false
	}
	for _, verb := range []string{"get", "patch"} {
		if !allows(verb, "astronomer-agent-identity") {
			t.Fatalf("active identity must allow %s", verb)
		}
		if allows(verb, "arbitrary-secret") {
			t.Fatalf("arbitrary existing Secret unexpectedly allows %s", verb)
		}
	}
	if !allows("get", "astronomer-agent-registration-token") || allows("patch", "astronomer-agent-registration-token") {
		t.Fatal("bootstrap must be exact-name read-only")
	}
	if !allows("get", "astronomer-agent-token") || !allows("patch", "astronomer-agent-token") {
		t.Fatal("legacy identity requires exact-name read plus annotation scrub patch")
	}
	if !allows("get", "astronomer-agent-ca") || allows("patch", "astronomer-agent-ca") {
		t.Fatal("agent CA must be exact-name readable for guarded decommission and never patchable")
	}
	for _, name := range []string{"astronomer-agent-registration-token", "astronomer-agent-identity", "astronomer-agent-token", "astronomer-agent-ca"} {
		if !allows("delete", name) {
			t.Fatalf("full decommission must allow exact-name delete of %s", name)
		}
	}
	if allows("delete", "arbitrary-secret") {
		t.Fatal("arbitrary Secret unexpectedly allows delete")
	}
	for _, verb := range []string{"create", "update", "list", "watch"} {
		if allows(verb, "astronomer-agent-identity") || allows(verb, "arbitrary-secret") {
			t.Fatalf("credential Role unexpectedly allows %s", verb)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
// TestSelfManagementRolePresentForEveryProfile is the core Phase 0 guard: the
// Astronomer self-management Role+RoleBinding (write within astronomer-* owned
// namespaces + patch the agent's own Deployment) is rendered for EVERY profile,
// viewer included. This is the second axis of the two-axis RBAC model — it is
// independent of the user-facing privilege profile.
func TestSelfManagementRolePresentForEveryProfile(t *testing.T) {
	profiles := []string{
		PrivilegeProfileAdmin,
		PrivilegeProfileViewer,
		PrivilegeProfileOperator,
		PrivilegeProfileNamespaceViewer,
		PrivilegeProfileNamespaceOperator,
		PrivilegeProfileCustom,
		"", // unspecified -> viewer default
	}
	for _, p := range profiles {
		manifest := RenderInstallYAML(InstallTemplateData{
			ServerURL:         "https://astro.example.com",
			ClusterID:         "c1",
			RegistrationToken: "tok",
			AgentImage:        "example.com/agent:v1",
			PrivilegeProfile:  p,
		})
		for _, want := range []string{
			// The self-management Role+RoleBinding exists for this profile.
			"name: astronomer-agent-self-management",
			// Bounded to EXACTLY the astronomer-owned namespaces (all 7).
			"namespace: astronomer-system",
			"namespace: astronomer-monitoring",
			"namespace: astronomer-trivy-system",
			"namespace: astronomer-logging",
			"namespace: astronomer-ingress-nginx",
			"namespace: astronomer-cert-manager",
			"namespace: astronomer-gatekeeper-system",
			// Write surface within those namespaces.
			`resources: ["daemonsets", "deployments", "replicasets", "statefulsets"]`,
			`verbs: ["create", "get", "list", "watch", "update", "patch", "delete"]`,
			// Own-Deployment patch, resourceName-scoped.
			`resourceNames: ["astronomer-agent"]`,
			`verbs: ["get", "list", "watch", "update", "patch"]`,
		} {
			if !strings.Contains(manifest, want) {
				t.Fatalf("profile %q self-management manifest missing %q:\n%s", p, want, manifest)
			}
		}
		// The phantom astronomer-baseline namespace must be gone entirely.
		if strings.Contains(manifest, "astronomer-baseline") {
			t.Fatalf("profile %q manifest still references removed astronomer-baseline namespace:\n%s", p, manifest)
		}
		// The self-management namespaced rules must NOT grant secrets write nor
		// any rbac.authorization.k8s.io write (no self-escalation). The only
		// secrets grant in the manifest is the resourceName-scoped token Role.
		// Parse the actual rules (not the comment text) to assert this.
		type smRule struct {
			APIGroups []string `yaml:"apiGroups"`
			Resources []string `yaml:"resources"`
		}
		var smRules []smRule
		if err := yaml.Unmarshal([]byte(SelfManagementNamespacedRulesYAML()), &smRules); err != nil {
			t.Fatalf("parse self-management namespaced rules: %v", err)
		}
		for _, r := range smRules {
			for _, g := range r.APIGroups {
				if g == "rbac.authorization.k8s.io" || g == "*" {
					t.Fatalf("self-management namespaced rules must not touch RBAC apiGroup %q (self-escalation)", g)
				}
			}
			for _, res := range r.Resources {
				if res == "secrets" || res == "*" {
					t.Fatalf("self-management namespaced rules must not grant %q write (token Secret is resourceName-scoped only)", res)
				}
			}
		}
		// The placeholders must be fully substituted.
		for _, unwanted := range []string{
			"{{AGENT_SELF_MANAGEMENT_NAMESPACED_RULES}}",
			"{{AGENT_SELF_MANAGEMENT_DEPLOYMENT_RULES}}",
		} {
			if strings.Contains(manifest, unwanted) {
				t.Fatalf("profile %q manifest still contains placeholder %q", p, unwanted)
			}
		}
	}
}

// TestSelfManagementNamespacesAreExactlyAstronomerOwned asserts the
// self-management Roles target precisely the astronomer-* owned namespace set
// and nothing else (no customer namespaces like default/kube-system, and no
// shared namespaces like velero/monitoring which are only selectively managed).
func TestSelfManagementNamespacesAreExactlyAstronomerOwned(t *testing.T) {
	type roleDoc struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
	}
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:         "https://astro.example.com",
		ClusterID:         "c1",
		RegistrationToken: "tok",
		AgentImage:        "example.com/agent:v1",
		PrivilegeProfile:  PrivilegeProfileViewer,
	})
	dec := yaml.NewDecoder(strings.NewReader(manifest))
	got := map[string]bool{}
	for {
		var d roleDoc
		err := dec.Decode(&d)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("decode manifest: %v", err)
		}
		if d.Kind == "Role" && d.Metadata.Name == "astronomer-agent-self-management" {
			got[d.Metadata.Namespace] = true
		}
	}
	want := map[string]bool{}
	for _, ns := range AstronomerOwnedNamespaces {
		want[ns] = true
	}
	if len(got) != len(want) {
		t.Fatalf("self-management Roles in namespaces %v, want exactly %v", got, want)
	}
	for ns := range want {
		if !got[ns] {
			t.Fatalf("missing self-management Role in astronomer-owned namespace %q (got %v)", ns, got)
		}
	}
	// Negative: must NOT target customer or shared namespaces.
	for _, forbidden := range []string{"default", "kube-system", "velero", "monitoring"} {
		if got[forbidden] {
			t.Fatalf("self-management Role unexpectedly targets non-owned namespace %q", forbidden)
		}
	}
}

// TestViewerSelfManagementDoesNotWidenUserProfile proves the two axes stay
// independent: with viewer selected, the self-management Role grants write
// within astronomer-* namespaces, but the user-facing ClusterRole (which reaches
// the customer's namespaces cluster-wide) stays strictly read-only. The only
// ClusterRoleBinding present is the read-only viewer one; all write lives in
// namespaced self-management Roles bound by RoleBindings.
func TestViewerSelfManagementDoesNotWidenUserProfile(t *testing.T) {
	// The viewer ClusterRole rules themselves must carry no write verbs and no
	// secrets — unchanged by the self-management addition.
	clusterRules := RBACRulesYAML(PrivilegeProfileViewer)
	for _, forbidden := range []string{`"create"`, `"update"`, `"patch"`, `"delete"`, `"secrets"`, `resources: ["*"]`, `verbs: ["*"]`} {
		if strings.Contains(clusterRules, forbidden) {
			t.Fatalf("viewer ClusterRole rules unexpectedly contain %q (user profile must stay read-only):\n%s", forbidden, clusterRules)
		}
	}

	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:         "https://astro.example.com",
		ClusterID:         "c1",
		RegistrationToken: "tok",
		AgentImage:        "example.com/agent:v1",
		PrivilegeProfile:  PrivilegeProfileViewer,
	})

	// The cluster-wide binding for viewer is a ClusterRoleBinding to the
	// read-only 'astronomer-agent' ClusterRole. Self-management write is granted
	// only via namespaced RoleBindings — never via a ClusterRole/ClusterRoleBinding.
	type bindingDoc struct {
		Kind    string `yaml:"kind"`
		RoleRef struct {
			Kind string `yaml:"kind"`
			Name string `yaml:"name"`
		} `yaml:"roleRef"`
	}
	dec := yaml.NewDecoder(strings.NewReader(manifest))
	for {
		var d bindingDoc
		err := dec.Decode(&d)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("decode manifest: %v", err)
		}
		// No binding may grant the self-management surface cluster-wide.
		if d.Kind == "ClusterRoleBinding" && d.RoleRef.Name == "astronomer-agent-self-management" {
			t.Fatalf("self-management must be namespaced, found a ClusterRoleBinding granting it cluster-wide")
		}
	}
}

// TestBootstrapRendersKubeStateMetricsReadOnlyClusterRole verifies that the
// bootstrap manifest's cluster-wide kube-state-metrics grant remains strictly
// read-only and binds only its astronomer-monitoring ServiceAccount.
func TestBootstrapRendersKubeStateMetricsReadOnlyClusterRole(t *testing.T) {
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:         "https://astro.example.com",
		ClusterID:         "c1",
		RegistrationToken: "tok",
		AgentImage:        "example.com/agent:v1",
		PrivilegeProfile:  PrivilegeProfileViewer,
	})

	type rbacRule struct {
		APIGroups []string `yaml:"apiGroups"`
		Resources []string `yaml:"resources"`
		Verbs     []string `yaml:"verbs"`
	}
	type clusterRoleDoc struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Rules   []rbacRule `yaml:"rules"`
		RoleRef struct {
			Kind string `yaml:"kind"`
			Name string `yaml:"name"`
		} `yaml:"roleRef"`
		Subjects []struct {
			Kind      string `yaml:"kind"`
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"subjects"`
	}

	writeVerbs := map[string]bool{
		"create": true, "update": true, "patch": true, "delete": true,
		"deletecollection": true, "*": true,
	}

	dec := yaml.NewDecoder(strings.NewReader(manifest))
	var sawRole, sawBinding bool
	for {
		var d clusterRoleDoc
		err := dec.Decode(&d)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("decode manifest: %v", err)
		}
		if d.Kind == "ClusterRole" && d.Metadata.Name == "astronomer-kube-state-metrics" {
			sawRole = true
			if len(d.Rules) == 0 {
				t.Fatal("ksm ClusterRole has no rules")
			}
			for _, r := range d.Rules {
				for _, v := range r.Verbs {
					if writeVerbs[v] {
						t.Fatalf("ksm ClusterRole grants write verb %q on %v (must be read-only)", v, r.Resources)
					}
					if v != "get" && v != "list" && v != "watch" {
						t.Fatalf("ksm ClusterRole grants unexpected verb %q on %v (only get/list/watch allowed)", v, r.Resources)
					}
				}
			}
		}
		if d.Kind == "ClusterRoleBinding" && d.Metadata.Name == "astronomer-kube-state-metrics" {
			sawBinding = true
			if d.RoleRef.Kind != "ClusterRole" || d.RoleRef.Name != "astronomer-kube-state-metrics" {
				t.Fatalf("ksm ClusterRoleBinding roleRef = %s/%s, want ClusterRole/astronomer-kube-state-metrics", d.RoleRef.Kind, d.RoleRef.Name)
			}
			if len(d.Subjects) != 1 ||
				d.Subjects[0].Kind != "ServiceAccount" ||
				d.Subjects[0].Name != "kube-state-metrics" ||
				d.Subjects[0].Namespace != "astronomer-monitoring" {
				t.Fatalf("ksm ClusterRoleBinding subjects = %+v, want the kube-state-metrics SA in astronomer-monitoring", d.Subjects)
			}
		}
	}
	if !sawRole {
		t.Fatal("bootstrap manifest missing astronomer-kube-state-metrics ClusterRole")
	}
	if !sawBinding {
		t.Fatal("bootstrap manifest missing astronomer-kube-state-metrics ClusterRoleBinding")
	}
}

func TestRenderInstallYAMLIsValidYAMLForEveryProfile(t *testing.T) {
	profiles := []string{
		PrivilegeProfileAdmin,
		PrivilegeProfileViewer,
		PrivilegeProfileOperator,
		PrivilegeProfileNamespaceViewer,
		PrivilegeProfileNamespaceOperator,
		PrivilegeProfileCustom,
		"", // unspecified -> viewer default
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

func TestRenderInstallYAMLBootstrapsSuspendedSignedSystemAfterAgent(t *testing.T) {
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL: "https://astro.example.test", ClusterID: "c1", RegistrationToken: "tok",
		AgentImage: "registry.example.test/agent@sha256:" + strings.Repeat("a", 64), PrivilegeProfile: PrivilegeProfileAdmin,
		SystemArtifactURL:    "registry.example.test/astronomer/system",
		SystemArtifactDigest: "sha256:" + strings.Repeat("b", 64),
		SystemOIDCIssuer:     "https://token.actions.githubusercontent.com",
		SystemOIDCIdentity:   "https://github.com/example/release/.github/workflows/release.yaml@refs/tags/v1.0.0",
	})
	for _, required := range []string{
		"kind: OCIRepository", "kind: Kustomization", "url: \"oci://registry.example.test/astronomer/system\"",
		"digest: \"sha256:" + strings.Repeat("b", 64) + "\"", "provider: cosign", "suspend: true",
		"serviceAccountName: astronomer-delivery-system-applier", "delivery.astronomer.io/system: \"true\"",
	} {
		if !strings.Contains(manifest, required) {
			t.Fatalf("registration manifest missing %q", required)
		}
	}
	agent := strings.Index(manifest, "kind: Deployment\nmetadata:\n  name: astronomer-agent")
	system := strings.LastIndex(manifest, "kind: OCIRepository")
	if agent < 0 || system < agent {
		t.Fatalf("system bootstrap must follow the agent Deployment: agent=%d system=%d", agent, system)
	}

	invalid := RenderInstallYAML(InstallTemplateData{ServerURL: "https://astro.example.test", ClusterID: "c1", RegistrationToken: "tok", AgentImage: "agent:v1", SystemArtifactURL: "oci://user:secret@example.test/system"})
	if strings.Contains(invalid, "name: astronomer-system-release") {
		t.Fatal("invalid or credential-bearing system source was rendered")
	}
}

func TestRenderInstallYAMLBootstrapsOfflinePinnedCosignKey(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded})
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL: "https://astro.example.test", ClusterID: "c1", RegistrationToken: "tok",
		AgentImage:           "registry.example.test/agent@sha256:" + strings.Repeat("a", 64),
		SystemArtifactURL:    "registry.internal.example.test/astronomer/system",
		SystemArtifactDigest: "sha256:" + strings.Repeat("b", 64),
		SystemPublicKey:      publicKey,
	})
	fingerprint := protocol.DeliverySystemKeyFingerprint(publicKey)
	for _, required := range []string{
		"name: astronomer-system-release-trust", "cosign.pub: " + base64.StdEncoding.EncodeToString(publicKey),
		"secretRef:\n      name: astronomer-system-release-trust", "ASTRONOMER_SYSTEM_KEY_FINGERPRINT",
		"value: \"" + fingerprint + "\"", "kind: OCIRepository", "kind: Kustomization",
	} {
		if !strings.Contains(manifest, required) {
			t.Fatalf("offline enrollment manifest missing %q", required)
		}
	}
	systemSource := strings.LastIndex(manifest, "kind: OCIRepository\nmetadata:\n  name: astronomer-system-release")
	if systemSource < 0 {
		t.Fatal("offline system OCIRepository was not rendered")
	}
	if strings.Contains(manifest[systemSource:], "matchOIDCIdentity") {
		t.Fatal("offline key policy also rendered a keyless OIDC verifier")
	}
	decoder := yaml.NewDecoder(strings.NewReader(manifest))
	documents := 0
	for {
		var doc any
		if err := decoder.Decode(&doc); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("offline enrollment manifest is invalid YAML at document %d: %v", documents, err)
		}
		documents++
	}
	if documents < 7 {
		t.Fatalf("offline enrollment rendered only %d YAML documents", documents)
	}

	nextKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	nextDER, err := x509.MarshalPKIXPublicKey(&nextKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	nextPublicKey := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: nextDER})
	keyringManifest := RenderInstallYAML(InstallTemplateData{
		ServerURL: "https://astro.example.test", ClusterID: "c2", RegistrationToken: "tok",
		AgentImage:        "registry.example.test/agent@sha256:" + strings.Repeat("a", 64),
		SystemArtifactURL: "registry.internal.example.test/astronomer/system", SystemArtifactDigest: "sha256:" + strings.Repeat("b", 64),
		SystemPublicKeys: [][]byte{publicKey, nextPublicKey},
	})
	keyringFingerprint := protocol.DeliverySystemKeyFingerprint(nextPublicKey)
	for _, required := range []string{
		"cosign.pub: " + base64.StdEncoding.EncodeToString(publicKey),
		"cosign-" + strings.TrimPrefix(keyringFingerprint, "sha256:") + ".pub: " + base64.StdEncoding.EncodeToString(nextPublicKey),
		"ASTRONOMER_SYSTEM_KEY_FINGERPRINTS", fingerprint + "," + keyringFingerprint,
	} {
		if !strings.Contains(keyringManifest, required) {
			t.Fatalf("overlapping offline enrollment keyring missing %q", required)
		}
	}

	mixed := RenderInstallYAML(InstallTemplateData{
		ServerURL: "https://astro.example.test", ClusterID: "c1", RegistrationToken: "tok",
		AgentImage: "agent:v1", SystemArtifactURL: "oci://registry.example.test/system",
		SystemArtifactDigest: "sha256:" + strings.Repeat("b", 64), SystemPublicKey: publicKey,
		SystemOIDCIssuer: "https://token.actions.githubusercontent.com", SystemOIDCIdentity: "release@example.test",
	})
	if strings.Contains(mixed, "name: astronomer-system-release\n") {
		t.Fatal("mixed key and OIDC trust rendered a system bootstrap")
	}
}

func TestRenderInstallYAMLCarriesNonSecretAgentTelemetry(t *testing.T) {
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL: "https://astro.example.test", ClusterID: "c1", RegistrationToken: "tok",
		AgentImage: "registry.example.test/agent:v1", OTELEndpoint: "http://tempo.observability.svc:4318",
		OTELInsecure: true, OTELSamplerRatio: "0", Environment: "staging",
	})
	for _, want := range []string{
		"- name: ASTRONOMER_ENV\n              value: \"staging\"",
		"- name: OTEL_EXPORTER_OTLP_ENDPOINT\n              value: \"http://tempo.observability.svc:4318\"",
		"- name: OTEL_EXPORTER_OTLP_INSECURE\n              value: \"true\"",
		"- name: OTEL_TRACES_SAMPLER_ARG\n              value: \"0\"",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("agent manifest missing telemetry setting %q", want)
		}
	}
	if strings.Contains(manifest, "OTEL_EXPORTER_OTLP_HEADERS") {
		t.Fatal("agent manifest must not copy management-plane collector credentials")
	}
}

// Legacy registration annotations must not recreate a read-only management mode.
func TestRenderInstallYAMLFullManagement(t *testing.T) {
	for _, profile := range []string{"", "viewer", "operator", "namespace-viewer", "namespace-operator", "custom", "admin", "unknown"} {
		t.Run(profile, func(t *testing.T) {
			manifest := RenderInstallYAML(InstallTemplateData{ServerURL: "https://astro.example.com", ClusterID: "c1", RegistrationToken: "token", AgentImage: "example.com/agent:v1", PrivilegeProfile: profile})
			decoder := yaml.NewDecoder(strings.NewReader(manifest))
			foundRole, foundBinding, foundConfig := false, false, false
			for {
				var doc struct {
					Kind     string `yaml:"kind"`
					Metadata struct {
						Name string `yaml:"name"`
					} `yaml:"metadata"`
					Rules []struct {
						APIGroups []string `yaml:"apiGroups"`
						Resources []string `yaml:"resources"`
						Verbs     []string `yaml:"verbs"`
					} `yaml:"rules"`
					RoleRef struct {
						Name string `yaml:"name"`
						Kind string `yaml:"kind"`
					} `yaml:"roleRef"`
					Data map[string]string `yaml:"data"`
				}
				if err := decoder.Decode(&doc); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				if doc.Kind == "ClusterRole" && doc.Metadata.Name == "astronomer-agent" {
					for _, rule := range doc.Rules {
						if strings.Join(rule.APIGroups, ",") == "*" && strings.Join(rule.Resources, ",") == "*" && strings.Join(rule.Verbs, ",") == "*" {
							foundRole = true
						}
					}
				}
				if doc.RoleRef.Name == "astronomer-agent" {
					if doc.Kind != "ClusterRoleBinding" || doc.RoleRef.Kind != "ClusterRole" {
						t.Fatalf("management binding is %s/%s", doc.Kind, doc.RoleRef.Kind)
					}
					foundBinding = true
				}
				if doc.Kind == "ConfigMap" && doc.Data["PRIVILEGE_PROFILE"] == "admin" {
					foundConfig = true
				}
			}
			if !foundRole || !foundBinding || !foundConfig {
				t.Fatalf("full management missing: role=%v binding=%v config=%v", foundRole, foundBinding, foundConfig)
			}
		})
	}
}
