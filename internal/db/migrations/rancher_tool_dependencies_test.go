package migrations_test

import (
	"strings"
	"testing"
)

func TestRancherToolDependencyMigration(t *testing.T) {
	up := readMigration(t, "069_align_rancher_tool_dependencies.up.sql")
	down := readMigration(t, "069_align_rancher_tool_dependencies.down.sql")

	for _, required := range []string{
		"WHERE slug = 'fluent-bit'",
		"'neuvector-crd'",
		"'release_name', 'neuvector-crd'",
		"'release_name', 'neuvector'",
		"'https://charts.rancher.io'",
		"'110.0.2+up2.11.2'",
		"'order', 0",
		"'order', 1",
	} {
		if !strings.Contains(up, required) {
			t.Errorf("dependency migration lacks %q", required)
		}
	}
	if strings.Contains(up, "astronomer-fluent-bit-config") {
		t.Fatal("standalone Fluent Bit Tool may not depend on the logging workflow ConfigMap")
	}
	for _, restored := range []string{"astronomer-fluent-bit-config", "https://neuvector.github.io/neuvector-helm", "'2.11.1'"} {
		if !strings.Contains(down, restored) {
			t.Errorf("rollback does not restore %q", restored)
		}
	}
}
