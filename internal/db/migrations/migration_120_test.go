package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration120File(t *testing.T, name string) string {
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

// TestClusterAgentTokenRotationMigrationContent asserts the rotation-grace
// columns (and their indexes) are added on up and dropped on down.
func TestClusterAgentTokenRotationMigrationContent(t *testing.T) {
	up := loadMigration120File(t, "120_cluster_agent_token_rotation.up.sql")
	down := loadMigration120File(t, "120_cluster_agent_token_rotation.down.sql")

	for _, want := range []string{
		"ADD COLUMN IF NOT EXISTS previous_token_hash TEXT",
		"ADD COLUMN IF NOT EXISTS rotation_pending_at TIMESTAMPTZ",
		"ADD COLUMN IF NOT EXISTS last_rotated_at TIMESTAMPTZ",
		"idx_cluster_agent_tokens_previous_token_hash",
		"idx_cluster_agent_tokens_last_rotated_at",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("120 up migration missing required content %q", want)
		}
	}

	for _, want := range []string{
		"DROP COLUMN IF EXISTS previous_token_hash",
		"DROP COLUMN IF EXISTS rotation_pending_at",
		"DROP COLUMN IF EXISTS last_rotated_at",
		"DROP INDEX IF EXISTS idx_cluster_agent_tokens_previous_token_hash",
		"DROP INDEX IF EXISTS idx_cluster_agent_tokens_last_rotated_at",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("120 down migration missing required content %q", want)
		}
	}
}
