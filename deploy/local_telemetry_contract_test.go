package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLocalTelemetryComposeContract(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "deploy", "docker-compose.telemetry.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var compose struct {
		Services map[string]struct {
			Image       string         `yaml:"image"`
			Profiles    []string       `yaml:"profiles"`
			Environment map[string]any `yaml:"environment"`
			MemLimit    string         `yaml:"mem_limit"`
			CPUs        float64        `yaml:"cpus"`
			Volumes     []string       `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &compose); err != nil {
		t.Fatalf("parse telemetry compose: %v", err)
	}
	for _, name := range []string{"tempo", "loki", "promtail", "prometheus", "grafana"} {
		service, ok := compose.Services[name]
		if !ok {
			t.Fatalf("telemetry compose missing %s", name)
		}
		if len(service.Profiles) != 1 || service.Profiles[0] != "telemetry" {
			t.Fatalf("%s profile = %v, want telemetry", name, service.Profiles)
		}
		if service.Image == "" || service.MemLimit == "" || service.CPUs <= 0 {
			t.Fatalf("%s lacks pinned image or resource bounds: %+v", name, service)
		}
	}
	server := compose.Services["server"]
	if server.Environment["OTEL_EXPORTER_OTLP_ENDPOINT"] == server.Environment["AGENT_OTEL_EXPORTER_OTLP_ENDPOINT"] {
		t.Fatal("management and adopted-cluster collector routes must be independent")
	}
	if _, copied := server.Environment["AGENT_OTEL_EXPORTER_OTLP_HEADERS"]; copied {
		t.Fatal("management collector credentials must not be copied into agent manifests")
	}
	promtail := compose.Services["promtail"]
	var readOnlyDockerSocket bool
	for _, volume := range promtail.Volumes {
		if volume == "/var/run/docker.sock:/var/run/docker.sock:ro" {
			readOnlyDockerSocket = true
		}
	}
	if !readOnlyDockerSocket {
		t.Fatal("local Promtail discovery must mount the Docker socket read-only")
	}
}

func TestLocalTelemetryConfigurationParses(t *testing.T) {
	root := repoRoot(t)
	for _, relative := range []string{
		"deploy/telemetry/tempo.yaml",
		"deploy/telemetry/loki.yaml",
		"deploy/telemetry/prometheus.yaml",
		"deploy/telemetry/promtail.yaml",
		"deploy/telemetry/grafana/provisioning/datasources/datasources.yaml",
	} {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", relative, err)
		}
		if strings.TrimSpace(string(raw)) == "" || doc == nil {
			t.Fatalf("%s is empty", relative)
		}
	}
}
