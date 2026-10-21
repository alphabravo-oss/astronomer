package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"sigs.k8s.io/yaml"
)

// Evaluate the actual chart rule against healthy queuing, transient latency,
// sustained saturation and idle counters. Brief pool waits are not exhaustion.
func TestDBPoolAlertPromQL(t *testing.T) {
	t.Parallel()
	docs := parseRenderedDocs(t, helmTemplateWithValueFilesAndFlags(t, nil, nil, "metrics.serviceMonitor.enabled=true"))
	resource := findRenderedDoc(t, docs, "PrometheusRule", "astronomer")
	var alert map[string]any
	for _, group := range nestedMap(resource, "spec")["groups"].([]any) {
		for _, raw := range group.(map[string]any)["rules"].([]any) {
			rule := raw.(map[string]any)
			if rule["alert"] == "AstronomerDBPoolExhausted" {
				alert = rule
			}
		}
	}
	if alert == nil {
		t.Fatal("pool saturation rule missing")
	}
	rules, err := yaml.Marshal(map[string]any{"groups": []any{map[string]any{"name": "db-pool", "rules": []any{alert}}}})
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy/testdata/db-pool-alert.test.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	promtool, err := exec.LookPath("promtool")
	if err != nil {
		t.Skip("promtool unavailable; pool alert evaluation NOT RUN")
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{"rules.yaml": rules, "tests.yaml": fixtures} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(promtool, "test", "rules", "tests.yaml")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pool alert evaluation: %v\n%s", err, output)
	}
}
