package agenttemplate

import (
	"strings"
	"testing"
)

func TestInstallManifestScopesDirectKubeconfigIssuer(t *testing.T) {
	t.Parallel()
	manifest := RenderInstallYAML(InstallTemplateData{
		ServerURL:         "https://management.example.test",
		ClusterID:         "00000000-0000-4000-8000-000000000001",
		RegistrationToken: "registration-material",
		AgentImage:        "example.test/astronomer-agent:v1.2.3",
		PrivilegeProfile:  PrivilegeProfileViewer,
	})
	for _, want := range []string{
		"name: astronomer-direct-reader",
		`resources: ["serviceaccounts/token"]`,
		`resourceNames: ["astronomer-direct-reader"]`,
		`verbs: ["create"]`,
		"automountServiceAccountToken: false",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing direct-access fence %q", want)
		}
	}
	issuerStart := strings.Index(manifest, "name: astronomer-direct-kubeconfig-issuer")
	if issuerStart < 0 {
		t.Fatal("issuer Role missing")
	}
	issuerEnd := strings.Index(manifest[issuerStart:], "\n---")
	if issuerEnd < 0 {
		t.Fatal("issuer Role terminator missing")
	}
	issuer := manifest[issuerStart : issuerStart+issuerEnd]
	for _, forbidden := range []string{`resources: ["secrets"]`, `resources: ["serviceaccounts"]`, `verbs: ["*"]`} {
		if strings.Contains(issuer, forbidden) {
			t.Fatalf("issuer Role unexpectedly contains %q", forbidden)
		}
	}
}
