package deploy

import (
	"os"
	"path/filepath"
	"testing"
)

// Locks the value shape emitted by buildSelfManagedAstronomerValues to the
// chart's real image helpers. A fully qualified runtime reference is split
// into registry/repository/tag and the global mirror is explicitly cleared;
// rendering must reconstruct each original reference exactly once.
func TestSelfManagedImageValuesRenderWithoutRegistryRePrefix(t *testing.T) {
	inherited := `
image:
  registry: stale.example/global-mirror
`
	values := `
image:
  registry: ""
  server:
    registry: localastro
    repository: astronomer-go-server
    tag: live
  worker:
    registry: ghcr.io/acme
    repository: astronomer-go-worker
    tag: worker-v2
  migrate:
    registry: registry.example:5000/team/platform
    repository: astronomer-go-migrate
    tag: live
  agent:
    registry: agents.registry:5001/nested/team
    repository: astronomer-go-agent
    tag: agent-v4
frontend:
  image:
    registry: frontend.registry:5443/org
    repository: astronomer-frontend
    tag: frontend-v5
config:
  agentImageRepository: agents.registry:5001/nested/team/astronomer-go-agent
  agentImageTag: agent-v4
preflight:
  enabled: false
`
	tempDir := t.TempDir()
	inheritedPath := filepath.Join(tempDir, "inherited-global-registry.yaml")
	if err := os.WriteFile(inheritedPath, []byte(inherited), 0o600); err != nil {
		t.Fatalf("write inherited values: %v", err)
	}
	valuesPath := filepath.Join(tempDir, "self-managed-images.yaml")
	if err := os.WriteFile(valuesPath, []byte(values), 0o600); err != nil {
		t.Fatalf("write values: %v", err)
	}

	docs := parseRenderedDocs(t, helmTemplateWithValueFiles(t, []string{inheritedPath, valuesPath}))
	assertRenderedContainerImage(t, docs, "Deployment", "astronomer-server", "containers", "server", "localastro/astronomer-go-server:live")
	assertRenderedContainerImage(t, docs, "Deployment", "astronomer-server", "initContainers", "migrate", "registry.example:5000/team/platform/astronomer-go-migrate:live")
	assertRenderedContainerImage(t, docs, "Deployment", "astronomer-worker", "containers", "worker", "ghcr.io/acme/astronomer-go-worker:worker-v2")
	assertRenderedContainerImage(t, docs, "Deployment", "astronomer-frontend", "containers", "frontend", "frontend.registry:5443/org/astronomer-frontend:frontend-v5")

	configMap := findRenderedDoc(t, docs, "ConfigMap", "astronomer-config")
	data := nestedMap(configMap, "data")
	if got := stringValue(data["AGENT_IMAGE_REPOSITORY"]); got != "agents.registry:5001/nested/team/astronomer-go-agent" {
		t.Fatalf("AGENT_IMAGE_REPOSITORY = %q", got)
	}
	if got := stringValue(data["AGENT_IMAGE_TAG"]); got != "agent-v4" {
		t.Fatalf("AGENT_IMAGE_TAG = %q", got)
	}
}

func assertRenderedContainerImage(t *testing.T, docs []renderedDoc, kind, workload, field, container, want string) {
	t.Helper()
	doc := findRenderedDoc(t, docs, kind, workload)
	got := stringValue(findContainer(t, podSpecFor(doc), field, container)["image"])
	if got != want {
		t.Fatalf("%s/%s %s %q image = %q, want %q", kind, workload, field, container, got, want)
	}
}
