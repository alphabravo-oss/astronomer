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

	manifest := h.renderAgentInstallManifest(cluster, "reg-token", "https://astro.example.com")

	checks := []string{
		"name: astronomer-system",
		`SERVER_URL: "https://astro.example.com"`,
		`CLUSTER_ID: "550e8400-e29b-41d4-a716-446655440000"`,
		`HEALTH_ADDR: ":8081"`,
		`token: "reg-token"`,
		`image: "example.com/astronomer-agent:v1.2.3"`,
		"- connect",
		"ASTRONOMER_AGENT_TOKEN",
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
	for _, unwanted := range []string{"--server-url", "--token", "--cluster-id", "ASTRONOMER_TOKEN", "HEALTH_PORT"} {
		if strings.Contains(manifest, unwanted) {
			t.Fatalf("manifest still contains obsolete %q", unwanted)
		}
	}
}

func TestRenderAgentInstallManifestHonorsPrivilegeProfileAnnotation(t *testing.T) {
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

	manifest := h.renderAgentInstallManifest(cluster, "reg-token", "https://astro.example.com")
	if !strings.Contains(manifest, `verbs: ["get", "list", "watch"]`) {
		t.Fatalf("viewer RBAC not rendered:\n%s", manifest)
	}
	if strings.Contains(manifest, `resources: ["*"]`) || strings.Contains(manifest, `verbs: ["*"]`) {
		t.Fatalf("viewer manifest rendered admin wildcard RBAC:\n%s", manifest)
	}
}

func TestRenderAgentInstallManifestHonorsAgentInstallMetadataAnnotations(t *testing.T) {
	h := NewClusterHandler(nil)
	h.SetAgentImage("example.com/default-agent", "v1.2.3")

	podLabels, err := json.Marshal(map[string]string{"team": "platform"})
	if err != nil {
		t.Fatalf("marshal pod labels: %v", err)
	}
	annotations, err := json.Marshal(map[string]string{
		agenttemplate.AgentImageAnnotation:              "registry.example.com/agent:v9",
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

	manifest := h.renderAgentInstallManifest(cluster, "reg-token", "https://astro.example.com")
	for _, want := range []string{
		`image: "registry.example.com/agent:v9"`,
		"name: team-agent",
		"serviceAccountName: team-agent",
		`team: "platform"`,
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing %q:\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "example.com/default-agent:v1.2.3") {
		t.Fatalf("manifest did not use image annotation override:\n%s", manifest)
	}
}
