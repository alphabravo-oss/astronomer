package migrations_test

// Static content test for migration 063_read_audit.
//
// Structural invariants:
//
//   - audit_log gains a NOT NULL action_class column with a DEFAULT
//     (the migration-safety lint requires NOT NULL columns to carry a
//     DEFAULT so the schema scan doesn't lock the table).
//   - The CHECK constraint pins the allowed class values.
//   - The 8 seed policies land in the read_audit_policies table.
//   - The DOWN drops read_audit_policies BEFORE removing the column /
//     constraint on audit_log.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration063File(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	path := filepath.Join(dir, name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestMigration_ReadAudit_UpContent(t *testing.T) {
	up := loadMigration063File(t, "063_read_audit.up.sql")

	for _, want := range []string{
		"ALTER TABLE audit_log",
		"ADD COLUMN IF NOT EXISTS action_class VARCHAR(16) NOT NULL DEFAULT 'mutation'",
		"audit_action_class_valid",
		"CHECK (action_class IN ('mutation','read','auth','system'))",
		"UPDATE audit_log SET action_class = 'auth' WHERE action LIKE 'auth.%'",
		"CREATE INDEX IF NOT EXISTS idx_audit_log_class",
		"CREATE TABLE IF NOT EXISTS read_audit_policies",
		"path_pattern    VARCHAR(256) NOT NULL",
		"sample_rate     NUMERIC(3,2) NOT NULL DEFAULT 1.00",
		"CONSTRAINT sample_rate_valid CHECK (sample_rate >= 0.0 AND sample_rate <= 1.0)",
		"INSERT INTO read_audit_policies",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}

	// Eight seeded policies — count `('` openers on the INSERT rows.
	for _, seed := range []string{
		"'cloud_credentials_read'",
		"'registry_credentials_read'",
		"'sso_secrets_read'",
		"'webhook_auth_read'",
		"'siem_auth_read'",
		"'audit_log_read'",
		"'support_bundle_download'",
		"'admin_settings_read'",
	} {
		if !strings.Contains(up, seed) {
			t.Errorf("up migration missing seed policy %q", seed)
		}
	}
}

func TestMigration_ReadAudit_DownContent(t *testing.T) {
	down := loadMigration063File(t, "063_read_audit.down.sql")
	for _, want := range []string{
		"DROP TABLE IF EXISTS read_audit_policies",
		"DROP INDEX IF EXISTS idx_audit_log_class",
		"DROP CONSTRAINT IF EXISTS audit_action_class_valid",
		"DROP COLUMN IF EXISTS action_class",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}
	// The policies table must drop before we strip the audit_log column.
	posTable := strings.Index(down, "DROP TABLE IF EXISTS read_audit_policies")
	posCol := strings.Index(down, "DROP COLUMN IF EXISTS action_class")
	if posTable < 0 || posCol < 0 {
		t.Fatal("missing expected DROP statements")
	}
	if posTable > posCol {
		t.Errorf("read_audit_policies dropped AFTER action_class column; rollback ordering broken")
	}
}
