package tasks

import (
	"context"
	"strings"
	"testing"
)

func TestRenderAgentManifestUsesSharedTemplate(t *testing.T) {
	manifest := renderAgentManifest(
		context.Background(),
		"550e8400-e29b-41d4-a716-446655440000",
		"reg-token",
		"https://astro.example.com",
		"example.com/astronomer-agent",
		"v1.2.3",
	)

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
	for _, unwanted := range []string{"--server-url", "--token", "--cluster-id", "ASTRONOMER_TOKEN", "ASTRONOMER_AGENT_TOKEN", "HEALTH_PORT"} {
		if strings.Contains(manifest, unwanted) {
			t.Fatalf("manifest still contains obsolete %q", unwanted)
		}
	}
}

func TestRenderAgentManifestPreservesReleaseDigest(t *testing.T) {
	digestRef := "example.com/astronomer-agent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	manifest := renderAgentManifest(context.Background(), "cluster", "token", "https://astro.example.com", digestRef, "v1.2.3")
	if !strings.Contains(manifest, `image: "`+digestRef+`"`) {
		t.Fatalf("manifest does not preserve agent digest")
	}
	if strings.Contains(manifest, digestRef+":v1.2.3") {
		t.Fatal("manifest appended the display version after an immutable digest")
	}
}

func TestRenderAgentManifestUsesFullManagementForLegacyProfile(t *testing.T) {
	manifest := renderAgentManifest(context.Background(), "cluster", "token", "https://astro.example.com", "example.com/agent", "v1", "operator")
	if !strings.Contains(manifest, `PRIVILEGE_PROFILE: "admin"`) || !strings.Contains(manifest, `verbs: ["*"]`) {
		t.Fatal("legacy profile prevented full management")
	}
}
