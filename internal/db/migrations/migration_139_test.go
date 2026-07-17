package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestMigration139AuditArchiveClusterName(t *testing.T) {
	up, err := os.ReadFile("139_audit_archive_cluster_name.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("139_audit_archive_cluster_name.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"archived_cluster_name VARCHAR(255) NOT NULL DEFAULT ''",
		"COALESCE(NULLIF(c.display_name, ''), c.name, '')",
		"a.archived_cluster_id = c.id",
		"a.archived_cluster_name = ''",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("up migration missing %q", required)
		}
	}
	// The trap this migration exists to avoid: the backfill must write its
	// own column, never resource_name (non-cluster rows swept in via
	// detail->>'cluster_id' would be mislabeled as clusters).
	if strings.Contains(string(up), "SET resource_name") {
		t.Fatal("up migration must not write resource_name")
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS archived_cluster_name") {
		t.Fatal("down migration missing DROP COLUMN IF EXISTS archived_cluster_name")
	}
}
