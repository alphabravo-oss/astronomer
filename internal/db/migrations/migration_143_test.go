package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestMigration143BaselineChartVersionPins(t *testing.T) {
	up, err := os.ReadFile("143_baseline_chart_version_pins.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Add-if-missing, builtin-only. An unconditional UPDATE would re-pin a
	// cluster the operator had deliberately held at another version.
	for _, required := range []string{
		"is_builtin = true",
		"slug = 'kube-state-metrics'",
		"slug = 'prometheus-node-exporter'",
		"coalesce(trim(version_constraint), '') = ''",
		"version_constraint = '8.0.0'",
		"version_constraint = '4.56.1'",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("up migration missing %q", required)
		}
	}
	// The pins must never be the floating constraint the change exists to
	// remove, and the opt-in components must not be pinned here — none of them
	// is DefaultEnabled, so none is auto-delivered as a baseline ApplicationSet.
	for _, forbidden := range []string{
		"version_constraint = '*'",
		"slug = 'trivy-operator'",
		"slug = 'fluent-bit'",
		"slug = 'ingress-nginx'",
		"slug = 'cert-manager'",
		"slug = 'gatekeeper'",
	} {
		if strings.Contains(string(up), forbidden) {
			t.Fatalf("up migration must not contain %q", forbidden)
		}
	}
}
