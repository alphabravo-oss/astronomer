package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration121File(t *testing.T, name string) string {
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

// TestClusterAgentTokenAdoptedMigrationContent asserts task A3's migration adds
// the adopted_at column, backfills only non-revoked non-empty-hash durables,
// and promotes the registration-token hash index to PARTIAL UNIQUE (the
// WHERE token_hash <> ” clause is mandatory — legacy plaintext rows carry an
// empty hash and would collide under a full unique index). Down reverses both.
func TestClusterAgentTokenAdoptedMigrationContent(t *testing.T) {
	up := loadMigration121File(t, "121_cluster_agent_token_adopted.up.sql")
	down := loadMigration121File(t, "121_cluster_agent_token_adopted.down.sql")

	for _, want := range []string{
		"ADD COLUMN IF NOT EXISTS adopted_at TIMESTAMPTZ",
		"COALESCE(last_used_at, last_rotated_at, created_at)",
		"WHERE adopted_at IS NULL AND revoked_at IS NULL AND token_hash <> ''",
		// no-lockout: a mid-join (minted-but-not-yet-adopted) durable, where
		// last_used_at still equals created_at, must NOT be backfill-stamped.
		"AND last_used_at > created_at",
		"DROP INDEX IF EXISTS idx_cluster_registration_tokens_token_hash",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_cluster_registration_tokens_token_hash",
		"WHERE token_hash <> ''",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("121 up migration missing required content %q", want)
		}
	}

	// The promoted index MUST be partial: a full UNIQUE would collide on the
	// shared empty hash of legacy plaintext rows.
	if !strings.Contains(up, "CREATE UNIQUE INDEX") || !strings.Contains(up, "ON cluster_registration_tokens (token_hash)\n    WHERE token_hash <> ''") {
		t.Error("121 up must create a PARTIAL unique index (WHERE token_hash <> '')")
	}

	for _, want := range []string{
		"DROP INDEX IF EXISTS idx_cluster_registration_tokens_token_hash",
		"CREATE INDEX IF NOT EXISTS idx_cluster_registration_tokens_token_hash",
		"DROP COLUMN IF EXISTS adopted_at",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("121 down migration missing required content %q", want)
		}
	}
	// Down must restore a NON-unique index (no UNIQUE keyword on the recreate).
	if strings.Contains(down, "CREATE UNIQUE INDEX") {
		t.Error("121 down must restore the NON-unique index, not a unique one")
	}
}
