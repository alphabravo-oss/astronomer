package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration107File(t *testing.T, name string) string {
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

func TestSeedBuiltinPSATemplatesMigrationContent(t *testing.T) {
	up := loadMigration107File(t, "107_seed_builtin_psa_templates.up.sql")
	down := loadMigration107File(t, "107_seed_builtin_psa_templates.down.sql")

	for _, want := range []string{
		// Schema change: the is_builtin flag.
		"ADD COLUMN IF NOT EXISTS is_builtin BOOLEAN NOT NULL DEFAULT false",
		// The three seeded starter templates, all flagged built-in.
		"'Privileged (PSA off)'",
		"'Baseline'",
		"'Restricted'",
		"INSERT INTO pod_security_templates",
		"ON CONFLICT (name) DO NOTHING",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("107 up migration missing required content %q", want)
		}
	}

	// "delivered but not enabled": the up migration must NOT create any
	// cluster_security_policies rows.
	if strings.Contains(up, "cluster_security_policies") &&
		strings.Contains(up, "INSERT INTO cluster_security_policies") {
		t.Errorf("107 up migration must not insert cluster_security_policies rows (templates are delivered, not enabled)")
	}

	for _, want := range []string{
		"DELETE FROM pod_security_templates WHERE is_builtin = true",
		"DROP COLUMN IF EXISTS is_builtin",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("107 down migration missing required content %q", want)
		}
	}
}
