package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration103File(t *testing.T, name string) string {
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

func TestClusterAgentTokenRevocationMigrationContent(t *testing.T) {
	up := loadMigration103File(t, "103_cluster_agent_token_revocation.up.sql")
	down := loadMigration103File(t, "103_cluster_agent_token_revocation.down.sql")

	for _, want := range []string{
		"ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ",
		"CREATE INDEX IF NOT EXISTS idx_cluster_agent_tokens_revoked_at",
		"ON cluster_agent_tokens (revoked_at)",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("103 up migration missing required content %q", want)
		}
	}

	for _, want := range []string{
		"DROP INDEX IF EXISTS idx_cluster_agent_tokens_revoked_at",
		"DROP COLUMN IF EXISTS revoked_at",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("103 down migration missing required content %q", want)
		}
	}
}
