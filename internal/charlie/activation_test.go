package charlie

import (
	"context"
	"crypto/ed25519"
	"net"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRuntimeActivatorCreatesDormantThenAgentSecrets(t *testing.T) {
	client := fake.NewSimpleClientset()
	activator, err := NewKubernetesRuntimeActivator(client, "astronomer", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Namespaces().Get(context.Background(), agentNamespaceName, metav1.GetOptions{}); err == nil {
		t.Fatal("Charlie agent namespace must not exist before activation")
	}
	if err := activator.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Namespaces().Get(context.Background(), agentNamespaceName, metav1.GetOptions{}); err != nil {
		t.Fatalf("prepare did not create agent namespace: %v", err)
	}

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	request := ActivationRequest{
		Validated: ValidatedOnboarding{
			RawPackage: []byte(`{"schema":"charlie.onboarding/v1"}`), SigningPublicKey: pub,
			ArtifactCredential: "ca_test", Package: activationPackage(),
		},
		Trust: GeneratedLocalTrust{
			Public: LocalPublicTrust{
				CACertificatePEM: "ca", BridgeClientCertificate: "bridge-client", MCPServerCertificate: "mcp-server",
			},
			Agent: AgentTrustMaterial{
				CACertificatePEM: "ca", BridgeServerCertificate: "bridge-server", BridgeServerPrivateKey: "bridge-key",
				MCPClientCertificate: "mcp-client", MCPClientPrivateKey: "mcp-key",
			},
			Astronomer: AstronomerTrustMaterial{BridgeClientPrivateKey: "bridge-client-key", MCPServerPrivateKey: "mcp-server-key"},
		},
		InstallationID:   "11111111-1111-4111-8111-111111111111",
		ProductNamespace: "astronomer",
	}
	if err := activator.Activate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{enrollmentSecret, bridgeTLSSecret, mcpTLSSecret, centralCASecret, artifactPullSecret} {
		if _, err := client.CoreV1().Secrets(agentNamespaceName).Get(context.Background(), name, metav1.GetOptions{}); err != nil {
			t.Fatalf("missing agent secret %s: %v", name, err)
		}
	}
	if _, err := client.CoreV1().Secrets("astronomer").Get(context.Background(), productMCPSecret, metav1.GetOptions{}); err != nil {
		t.Fatalf("missing product MCP secret: %v", err)
	}
	if _, err := client.CoreV1().Services("astronomer").Get(context.Background(), mcpServiceName, metav1.GetOptions{}); err != nil {
		t.Fatalf("missing MCP service: %v", err)
	}
}

type recordingHelm struct{ uninstalls int }

func (recordingHelm) Apply(context.Context, HelmReleaseSpec) error { return nil }
func (h *recordingHelm) Uninstall(context.Context) error           { h.uninstalls++; return nil }

func TestRuntimeDeactivateRemovesAgentAndProductMaterial(t *testing.T) {
	original := lookupCharlieIPs
	lookupCharlieIPs = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.10")}, nil }
	t.Cleanup(func() { lookupCharlieIPs = original })
	client := fake.NewSimpleClientset()
	helm := &recordingHelm{}
	activator, err := NewKubernetesRuntimeActivator(client, "astronomer", helm)
	if err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := activator.Activate(context.Background(), ActivationRequest{
		Validated: ValidatedOnboarding{
			RawPackage: []byte(`{"schema":"charlie.onboarding/v1"}`), SigningPublicKey: pub,
			ArtifactCredential: "ca_test", Package: activationPackage(),
		},
		Trust: GeneratedLocalTrust{
			Public: LocalPublicTrust{
				CACertificatePEM: "ca", BridgeClientCertificate: "bridge-client", MCPServerCertificate: "mcp-server",
			},
			Agent: AgentTrustMaterial{
				CACertificatePEM: "ca", BridgeServerCertificate: "bridge-server", BridgeServerPrivateKey: "bridge-key",
				MCPClientCertificate: "mcp-client", MCPClientPrivateKey: "mcp-key",
			},
			Astronomer: AstronomerTrustMaterial{BridgeClientPrivateKey: "bridge-client-key", MCPServerPrivateKey: "mcp-server-key"},
		},
		InstallationID:   "11111111-1111-4111-8111-111111111111",
		ProductNamespace: "astronomer",
	}); err != nil {
		t.Fatal(err)
	}
	if err := activator.Deactivate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if helm.uninstalls != 1 {
		t.Fatalf("helm uninstalls=%d", helm.uninstalls)
	}
	if _, err := client.CoreV1().Namespaces().Get(context.Background(), agentNamespaceName, metav1.GetOptions{}); err == nil {
		t.Fatal("agent namespace survived disconnect")
	}
	if _, err := client.CoreV1().Secrets("astronomer").Get(context.Background(), productMCPSecret, metav1.GetOptions{}); err == nil {
		t.Fatal("product MCP secret survived disconnect")
	}
	if _, err := client.CoreV1().Services("astronomer").Get(context.Background(), mcpServiceName, metav1.GetOptions{}); err == nil {
		t.Fatal("MCP service survived disconnect")
	}
	if err := activator.Deactivate(context.Background()); err != nil {
		t.Fatalf("repeat disconnect must be idempotent: %v", err)
	}
}

func TestHelmSpecRequiresDigestPinnedImage(t *testing.T) {
	original := lookupCharlieIPs
	lookupCharlieIPs = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.10")}, nil }
	t.Cleanup(func() { lookupCharlieIPs = original })
	spec, err := helmSpec(ActivationRequest{
		Validated: ValidatedOnboarding{Package: activationPackage()}, InstallationID: "11111111-1111-4111-8111-111111111111",
		ProductNamespace: "astronomer",
	})
	if err != nil {
		t.Fatal(err)
	}
	central, _ := spec.Values["networkPolicy"].(map[string]any)["central"].(map[string]any)
	if cidrs, _ := central["cidrs"].([]any); len(cidrs) == 0 {
		t.Fatal("Charlie central egress CIDRs are required for the agent chart")
	}
	bad := activationPackage()
	bad.Artifact.Image = "charlie.example/agent:latest"
	if _, err := helmSpec(ActivationRequest{
		Validated: ValidatedOnboarding{Package: bad}, InstallationID: "11111111-1111-4111-8111-111111111111",
	}); err == nil {
		t.Fatal("unsigned tag must be rejected")
	}
}

func TestImageRegistryHost(t *testing.T) {
	if got := imageRegistryHost("charlie.dev.example/charlie/agent@sha256:abc"); got != "charlie.dev.example" {
		t.Fatalf("host=%q", got)
	}
}

func TestPrepareIsIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: agentNamespaceName}})
	activator, err := NewKubernetesRuntimeActivator(client, "astronomer", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := activator.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func activationPackage() contract.OnboardingPackage {
	var pkg contract.OnboardingPackage
	pkg.Artifact.Image = "registry.example.test/charlie/agent@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	pkg.Artifact.Chart = "oci://registry.example.test/charlie/agent-chart"
	pkg.Artifact.ChartDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	pkg.Central.BaseUrl = "https://charlie.example.test"
	pkg.Central.CaBundlePem = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"
	pkg.ProductSlug = "astronomer"
	pkg.LogicalAgentId = "agent-1"
	pkg.EnvironmentId = "development"
	pkg.TenantId = "tenant-1"
	pkg.ReplicaCount = 2
	return pkg
}
