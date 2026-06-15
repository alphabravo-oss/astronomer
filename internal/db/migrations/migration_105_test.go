package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration105File(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func TestPolicyAndIngressBaselineToolSeedMigrationContent(t *testing.T) {
	up := loadMigration105File(t, "105_seed_policy_and_ingress_baseline_tools.up.sql")
	down := loadMigration105File(t, "105_seed_policy_and_ingress_baseline_tools.down.sql")

	for _, want := range []string{
		"'ingress-nginx'",
		"'https://kubernetes.github.io/ingress-nginx'",
		"'gatekeeper'",
		"'https://open-policy-agent.github.io/gatekeeper/charts'",
		"ON CONFLICT (slug) DO NOTHING",
		"ON CONFLICT (name) DO NOTHING",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("105 up migration missing required content %q", want)
		}
	}

	for _, want := range []string{
		"'ingress-nginx'",
		"'gatekeeper'",
		"'open-policy-agent'",
		"DELETE FROM cluster_tools",
		"DELETE FROM helm_repositories",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("105 down migration missing required content %q", want)
		}
	}
}
