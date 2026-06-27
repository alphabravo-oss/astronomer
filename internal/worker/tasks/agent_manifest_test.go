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
	for _, unwanted := range []string{"--server-url", "--token", "--cluster-id", "ASTRONOMER_TOKEN", "HEALTH_PORT"} {
		if strings.Contains(manifest, unwanted) {
			t.Fatalf("manifest still contains obsolete %q", unwanted)
		}
	}
}

func TestRenderAgentManifestSupportsOperatorPrivilegeProfile(t *testing.T) {
	manifest := renderAgentManifest(
		context.Background(),
		"550e8400-e29b-41d4-a716-446655440000",
		"reg-token",
		"https://astro.example.com",
		"example.com/astronomer-agent",
		"v1.2.3",
		"operator",
	)
	for _, want := range []string{
		"pods/exec",
		// Operator can READ RBAC objects (write belongs to admin only); the
		// hardened rule lists cluster + namespaced RBAC resources together.
		`resources: ["clusterroles", "clusterrolebindings", "roles", "rolebindings"]`,
		`verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]`,
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("operator manifest missing %q", want)
		}
	}
	if strings.Contains(manifest, `resources: ["*"]`) || strings.Contains(manifest, `verbs: ["*"]`) {
		t.Fatalf("operator manifest rendered admin wildcard RBAC:\n%s", manifest)
	}
}
