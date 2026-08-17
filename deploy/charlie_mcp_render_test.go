package deploy

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

func TestCharlieMCPListenerScaffoldingIsDormantUntilAgentInstall(t *testing.T) {
	out := helmTemplate(t)
	for _, expected := range []string{
		`RELEASE_NAME: "astronomer"`, `CHART_VERSION: "1.0.0"`,
		"secretName: astronomer-charlie-mcp-tls", "optional: true",
		"mountPath: /var/run/secrets/astronomer/charlie-mcp",
		"name: CHARLIE_MCP_TLS_CERT_FILE", "name: CHARLIE_MCP_TLS_KEY_FILE", "name: CHARLIE_MCP_CLIENT_CA_FILE",
		"name: CHARLIE_MCP_ACTION_SIGNING_KEY_FILE",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("Charlie MCP chart contract missing %q", expected)
		}
	}
	// The MCP port is private Service-only. Public ingress templates must not
	// route 7444 or the MCP path.
	if strings.Contains(out, "path: /mcp") {
		t.Fatal("Charlie MCP listener was exposed through public ingress")
	}
	for _, forbidden := range []string{"containerPort: 7444", "targetPort: charlie-mcp", "port: 7443", "port: 7444"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("base chart exposes Charlie runtime scaffolding %q before installation", forbidden)
		}
	}
}

func TestCharliePrivatePortsAreNotInBaseNetworkPolicy(t *testing.T) {
	out := helmTemplate(t)
	if strings.Contains(out, "port: 7443") || strings.Contains(out, "port: 7444") {
		t.Fatal("base chart contains Charlie private ports before an owner-bound agent install")
	}
}

func TestCharlieExpiryAlertsAreContentFreeAndDeterministic(t *testing.T) {
	out := helmTemplate(t, "metrics.serviceMonitor.enabled=true")
	for _, expected := range []string{
		"alert: AstronomerCharlieCertificateExpiring", `kind="certificate"`,
		"alert: AstronomerCharlieEnrollmentExpiring", `kind="enrollment"`,
		"alert: AstronomerCharlieArtifactCredentialExpiring", `kind="artifact"`,
		"alert: AstronomerCharlieOnboardingPackageExpiring", `kind="onboarding_package"`,
		"astronomer_charlie_expiry_seconds", "component: charlie",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("Charlie expiry alert contract missing %q", expected)
		}
	}
	for _, forbidden := range []string{"central_url", "resource_id", "credential_value", "certificate_pem"} {
		if strings.Contains(out, "astronomer_charlie_expiry_seconds{"+forbidden) {
			t.Fatalf("Charlie expiry alert contains unbounded label %q", forbidden)
		}
	}
}

func TestCharlieNetworkIsolationIsExactInRestrictedRender(t *testing.T) {
	override := filepath.Join(t.TempDir(), "restricted-values.yaml")
	if err := os.WriteFile(override, []byte("networkPolicy:\n  externalEgressCIDRs: []\n  externalHTTPSEgressCIDRs: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := helmTemplateWithValueFiles(t, []string{override})
	policies := renderedNetworkPolicies(t, out)
	server := policyByComponent(t, policies, "server")
	worker := policyByComponent(t, policies, "worker")
	frontend := policyByComponent(t, policies, "frontend")

	for component, policy := range map[string]networkingv1.NetworkPolicy{"server": server, "worker": worker, "frontend": frontend} {
		if policyHasPort(policy, 7443) || policyHasPort(policy, 7444) {
			t.Fatalf("%s can reach a private Charlie port", component)
		}
	}
	for _, rule := range server.Spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock != nil {
				t.Fatalf("restricted server render retained external IP egress %s; direct Charlie Central access is not isolated", peer.IPBlock.CIDR)
			}
		}
	}

	decoder := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(out), 4096)
	for {
		var object map[string]any
		if err := decoder.Decode(&object); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		kind, _ := object["kind"].(string)
		if kind != "Ingress" && kind != "HTTPRoute" && kind != "Gateway" {
			continue
		}
		encoded, _ := json.Marshal(object)
		if strings.Contains(string(encoded), "7443") || strings.Contains(string(encoded), "7444") || strings.Contains(string(encoded), `"/mcp"`) {
			t.Fatalf("public %s exposes private Charlie listener: %s", kind, encoded)
		}
	}
}

func renderedNetworkPolicies(t *testing.T, rendered string) []networkingv1.NetworkPolicy {
	t.Helper()
	decoder := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(rendered), 4096)
	var policies []networkingv1.NetworkPolicy
	for {
		var object map[string]any
		if err := decoder.Decode(&object); err != nil {
			if err == io.EOF {
				return policies
			}
			t.Fatal(err)
		}
		if object["kind"] != "NetworkPolicy" {
			continue
		}
		encoded, _ := json.Marshal(object)
		var policy networkingv1.NetworkPolicy
		if err := json.Unmarshal(encoded, &policy); err != nil {
			t.Fatal(err)
		}
		policies = append(policies, policy)
	}
}

func policyByComponent(t *testing.T, policies []networkingv1.NetworkPolicy, component string) networkingv1.NetworkPolicy {
	t.Helper()
	for _, policy := range policies {
		if policy.Labels["app.kubernetes.io/component"] == component {
			return policy
		}
	}
	t.Fatalf("render lacks %s NetworkPolicy", component)
	return networkingv1.NetworkPolicy{}
}

func portsContain(ports []networkingv1.NetworkPolicyPort, want int32) bool {
	for _, port := range ports {
		if port.Port != nil && port.Port.Type == 0 && port.Port.IntVal == want {
			return true
		}
	}
	return false
}

func policyHasPort(policy networkingv1.NetworkPolicy, port int32) bool {
	for _, rule := range policy.Spec.Ingress {
		if portsContain(rule.Ports, port) {
			return true
		}
	}
	for _, rule := range policy.Spec.Egress {
		if portsContain(rule.Ports, port) {
			return true
		}
	}
	return false
}
