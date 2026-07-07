package migrations_test

import (
	"strings"
	"testing"
)

// TestMigration132CreatesAlertInhibitions asserts the P-03 inhibition table is
// created with JSONB matcher columns defaulting to '[]' (so inserts that omit
// them stay valid) and an enabled flag.
func TestMigration132CreatesAlertInhibitions(t *testing.T) {
	up := loadMigrationFile(t, "132_alert_inhibitions.up.sql")
	for _, needle := range []string{
		"CREATE TABLE alert_inhibitions",
		"source_matchers JSONB NOT NULL DEFAULT '[]'",
		"target_matchers JSONB NOT NULL DEFAULT '[]'",
		"equal_labels JSONB NOT NULL DEFAULT '[]'",
		"enabled BOOLEAN NOT NULL DEFAULT true",
	} {
		if !strings.Contains(up, needle) {
			t.Fatalf("migration 132 up missing %q", needle)
		}
	}
}

func TestMigration132DownDropsAlertInhibitions(t *testing.T) {
	down := loadMigrationFile(t, "132_alert_inhibitions.down.sql")
	if !strings.Contains(down, "DROP TABLE IF EXISTS alert_inhibitions") {
		t.Fatalf("migration 132 down missing DROP TABLE")
	}
}

// TestMigration133CreatesAuthoredConstraints asserts the P-04 authored-
// constraint table exists with a (cluster_id, name) uniqueness constraint so
// re-applying an authored constraint upserts rather than duplicating.
func TestMigration133CreatesAuthoredConstraints(t *testing.T) {
	up := loadMigrationFile(t, "133_authored_constraints.up.sql")
	for _, needle := range []string{
		"CREATE TABLE authored_constraints",
		"cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE",
		"UNIQUE (cluster_id, name)",
	} {
		if !strings.Contains(up, needle) {
			t.Fatalf("migration 133 up missing %q", needle)
		}
	}
}

func TestMigration133DownDropsAuthoredConstraints(t *testing.T) {
	down := loadMigrationFile(t, "133_authored_constraints.down.sql")
	if !strings.Contains(down, "DROP TABLE IF EXISTS authored_constraints") {
		t.Fatalf("migration 133 down missing DROP TABLE")
	}
}
