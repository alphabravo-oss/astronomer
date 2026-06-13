package migrations_test

import (
	"strings"
	"testing"
)

func TestMigration092TaskOutbox(t *testing.T) {
	up := loadMigrationFile(t, "092_task_outbox.up.sql")
	required := []string{
		"CREATE TABLE IF NOT EXISTS task_outbox",
		"dedupe_key            TEXT",
		"payload               BYTEA",
		"max_delivery_attempts INTEGER NOT NULL DEFAULT 20",
		"CONSTRAINT task_outbox_status_valid CHECK (status IN ('pending', 'delivering', 'failed', 'delivered', 'dead'))",
		"CREATE UNIQUE INDEX IF NOT EXISTS task_outbox_dedupe_key_unique",
		"WHERE dedupe_key IS NOT NULL",
		"CREATE INDEX IF NOT EXISTS task_outbox_due_idx",
		"WHERE status IN ('pending', 'failed', 'delivering')",
	}
	for _, needle := range required {
		if !strings.Contains(up, needle) {
			t.Fatalf("migration 092 missing %q", needle)
		}
	}
}

func TestMigration092DownDropsOutbox(t *testing.T) {
	down := loadMigrationFile(t, "092_task_outbox.down.sql")
	if !strings.Contains(down, "DROP TABLE IF EXISTS task_outbox") {
		t.Fatalf("migration 092 down must drop task_outbox")
	}
}
