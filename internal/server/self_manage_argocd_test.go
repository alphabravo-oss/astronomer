package server

import (
	"testing"

	"sigs.k8s.io/yaml"
)

func TestMergeSelfManagedValuesPreservesDesiredReplicas(t *testing.T) {
	current := `
server:
  replicaCount: 2
worker:
  replicaCount: 2
frontend:
  enabled: true
  replicaCount: 2
image:
  server:
    repository: astronomer-go-server
    tag: old
config:
  corsAllowedOrigins: http://old.example
`
	bootstrap := `
server:
  replicaCount: 1
worker:
  replicaCount: 1
frontend:
  enabled: true
  replicaCount: 1
image:
  server:
    repository: astronomer-go-server
    tag: new
config:
  corsAllowedOrigins: http://new.example
gateway:
  enabled: true
`

	mergedYAML, err := mergeSelfManagedValues(current, bootstrap)
	if err != nil {
		t.Fatalf("mergeSelfManagedValues returned error: %v", err)
	}

	var merged map[string]any
	if err := yaml.Unmarshal([]byte(mergedYAML), &merged); err != nil {
		t.Fatalf("unmarshal merged values: %v", err)
	}

	serverValues := merged["server"].(map[string]any)
	if got := serverValues["replicaCount"]; got != float64(2) && got != 2 {
		t.Fatalf("server replicaCount = %v, want 2", got)
	}
	workerValues := merged["worker"].(map[string]any)
	if got := workerValues["replicaCount"]; got != float64(2) && got != 2 {
		t.Fatalf("worker replicaCount = %v, want 2", got)
	}
	frontendValues := merged["frontend"].(map[string]any)
	if got := frontendValues["replicaCount"]; got != float64(2) && got != 2 {
		t.Fatalf("frontend replicaCount = %v, want 2", got)
	}
	configValues := merged["config"].(map[string]any)
	if got := configValues["corsAllowedOrigins"]; got != "http://new.example" {
		t.Fatalf("config.corsAllowedOrigins = %v, want updated bootstrap value", got)
	}
	imageValues := merged["image"].(map[string]any)
	serverImage := imageValues["server"].(map[string]any)
	if got := serverImage["tag"]; got != "new" {
		t.Fatalf("image.server.tag = %v, want new", got)
	}
	if _, ok := merged["gateway"]; !ok {
		t.Fatalf("gateway values not preserved from bootstrap set")
	}
}
