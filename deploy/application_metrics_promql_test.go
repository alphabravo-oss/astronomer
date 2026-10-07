package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"sigs.k8s.io/yaml"
)

// Evaluate rules extracted from the actual Helm render, never a duplicated
// handwritten expression. Keep evaluator absence visible as a skipped test.
func TestApplicationMetricsPromQL(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplateWithValueFilesAndFlags(t, nil, []string{"--namespace", "control"},
		"metrics.serviceMonitor.enabled=true", "metrics.prometheusRule.runbookBaseURL=https://example.test/runbooks"))
	alerts := applicationMetricsAlerts(t, findRenderedDoc(t, docs, "PrometheusRule", "astronomer"))
	rules, err := yaml.Marshal(map[string]any{"groups": []any{map[string]any{"name": "application-metrics", "rules": []any{alerts["server"], alerts["worker"]}}}})
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "testdata", "application-metrics.test.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Tests []map[string]any `json:"tests"`
	}
	if err := yaml.UnmarshalStrict(fixtures, &map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(fixtures, &suite); err != nil || len(suite.Tests) != 10 {
		t.Fatalf("expected ten PromQL scenarios: %v", err)
	}
	promtool, err := exec.LookPath("promtool")
	if err != nil {
		t.Skip("promtool unavailable; rendered fixtures validated, PromQL evaluation NOT RUN")
	}
	dir := t.TempDir()
	for name, content := range map[string][]byte{"rules.yaml": rules, "tests.yaml": fixtures} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(promtool, "test", "rules", "tests.yaml")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("promtool: %v\n%s", err, output)
	}
}
