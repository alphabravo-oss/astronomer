package handler

import (
	"encoding/json"
	"strings"
	"testing"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestRenderAgentInstallManifestUsesTemplate(t *testing.T) {
	h := NewClusterHandler(nil)
	h.SetAgentImage("example.com/astronomer-agent", "v1.2.3")

	cluster := sqlc.Cluster{
		ID:   uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		Name: "demo",
	}

	manifest := mustRenderAgentInstallManifest(t, h, cluster, "reg-token", "https://astro.example.com")

	checks := []string{
		"name: astronomer-system",
		`SERVER_URL: "https://astro.example.com"`,
		`CLUSTER_ID: "550e8400-e29b-41d4-a716-446655440000"`,
		`HEALTH_ADDR: ":8081"`,
		`token: "reg-token"`,
		`image: "example.com/astronomer-agent:v1.2.3"`,
		"- connect",
		"ASTRONOMER_IDENTITY_TOKEN_SECRET_NAME",
		`prometheus.io/port: "8081"`,
		"- port: 80",
		"- port: 443",
		"- port: 8080",
		"- port: 8443",
		"- port: 8081",
	}
	for _, want := range checks {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing %q", want)
		}
	}
	if strings.Contains(manifest, "placeholder") {
		t.Fatal("manifest still contains placeholder text")
	}
	for _, unwanted := range []string{"--server-url", "--token", "--cluster-id", "ASTRONOMER_TOKEN", "ASTRONOMER_AGENT_TOKEN", "HEALTH_PORT"} {
		if strings.Contains(manifest, unwanted) {
			t.Fatalf("manifest still contains obsolete %q", unwanted)
		}
	}
}

func TestRenderAgentInstallManifestPreservesReleaseDigest(t *testing.T) {
	digestRef := "example.com/astronomer-agent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	h := NewClusterHandler(nil)
	h.SetAgentImage(digestRef, "v1.2.3")
	cluster := sqlc.Cluster{ID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), Name: "demo"}
	manifest := mustRenderAgentInstallManifest(t, h, cluster, "reg-token", "https://astro.example.com")
	if !strings.Contains(manifest, `image: "`+digestRef+`"`) {
		t.Fatalf("manifest does not preserve agent digest")
	}
}

func TestRenderAgentInstallManifestCarriesConfiguredTraceRouting(t *testing.T) {
	h := NewClusterHandler(nil)
	h.SetAgentImage("example.com/astronomer-agent", "v1.2.3")
	h.SetAgentTelemetry("http://tempo.observability.svc:4318", true, 0, "production")
	cluster := sqlc.Cluster{ID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), Name: "demo"}
	manifest := mustRenderAgentInstallManifest(t, h, cluster, "reg-token", "https://astro.example.com")
	for _, want := range []string{
		`value: "http://tempo.observability.svc:4318"`,
		`value: "true"`,
		`value: "0"`,
		`value: "production"`,
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing trace setting %q", want)
		}
	}
}

func TestRenderAgentInstallManifestRetiresViewerAnnotation(t *testing.T) {
	h := NewClusterHandler(nil)
	h.SetAgentImage("example.com/astronomer-agent", "v1.2.3")

	annotations, err := json.Marshal(map[string]string{
		agenttemplate.PrivilegeProfileAnnotation: agenttemplate.PrivilegeProfileViewer,
	})
	if err != nil {
		t.Fatalf("marshal annotations: %v", err)
	}
	cluster := sqlc.Cluster{
		ID:          uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		Name:        "demo",
		Annotations: annotations,
	}

	manifest := mustRenderAgentInstallManifest(t, h, cluster, "reg-token", "https://astro.example.com")
	if !strings.Contains(manifest, `verbs: ["get", "list", "watch"]`) {
		t.Fatalf("viewer RBAC not rendered:\n%s", manifest)
	}
	if !strings.Contains(manifest, `PRIVILEGE_PROFILE: "admin"`) || !strings.Contains(manifest, `verbs: ["*"]`) {
		t.Fatal("legacy viewer annotation prevented full cluster management")
	}
}

func TestRenderAgentInstallManifestHonorsSafeAgentInstallMetadataAnnotations(t *testing.T) {
	h := NewClusterHandler(nil)
	h.SetAgentImage("example.com/default-agent", "v1.2.3")

	podLabels, err := json.Marshal(map[string]string{"team": "platform"})
	if err != nil {
		t.Fatalf("marshal pod labels: %v", err)
	}
	annotations, err := json.Marshal(map[string]string{
		// Image selection is release-controlled; this retired annotation must
		// not bypass the digest-pinned system-release trust boundary.
		"management.astronomer.io/agent-image":          "registry.example.com/agent:v9",
		agenttemplate.AgentServiceAccountNameAnnotation: "team-agent",
		agenttemplate.AgentPodLabelsAnnotation:          string(podLabels),
	})
	if err != nil {
		t.Fatalf("marshal annotations: %v", err)
	}
	cluster := sqlc.Cluster{
		ID:          uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		Name:        "demo",
		Annotations: annotations,
	}

	manifest := mustRenderAgentInstallManifest(t, h, cluster, "reg-token", "https://astro.example.com")
	for _, want := range []string{
		`image: "example.com/default-agent:v1.2.3"`,
		"name: team-agent",
		"serviceAccountName: team-agent",
		`team: "platform"`,
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing %q:\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "registry.example.com/agent:v9") {
		t.Fatal("deprecated cluster annotation overrode release-controlled agent image")
	}
}
