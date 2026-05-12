package migrations_test

// Static content test for migration 065_kubectl_sessions.
//
// As with the other migration_*_test.go siblings, we DO NOT run the
// migration against Postgres — the CI helm-test path covers that via
// the migrate-job container. What we check here is the SHAPE of the
// SQL, so an unrelated future edit can't quietly:
//
//   - Drop the FK ON DELETE CASCADE on user_id / cluster_id. Without
//     it, deleting a user or cluster would either fail (RESTRICT) or
//     leave dangling kubectl_sessions rows.
//   - Drop the partial expires-at index. Without it, the 60s reaper
//     degrades into a seq-scan on every tick.
//   - Drop the CHECK constraint on `status`. Without it, the worker
//     state machine could land in a typo'd terminal state and never
//     get cleaned up.
//   - Drop the session_commands FK CASCADE — losing it would orphan
//     audit rows when the parent session row is deleted.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration065File(t *testing.T, name string) string {
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

func TestMigration_KubectlSessions_UpContent(t *testing.T) {
	up := loadMigration065File(t, "065_kubectl_sessions.up.sql")

	for _, want := range []string{
		"CREATE TABLE kubectl_sessions",
		// PK on id (UUID).
		"id              UUID PRIMARY KEY DEFAULT gen_random_uuid()",
		// FK + CASCADE on user + cluster so deletes propagate cleanly.
		"user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE",
		"cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE",
		// Status check constraint — protects the worker state machine.
		"CONSTRAINT kubectl_status_valid CHECK (status IN ('starting','active','closed','expired','failed'))",
		// Required indexes.
		"CREATE INDEX idx_kubectl_sessions_user",
		"CREATE INDEX idx_kubectl_sessions_active",
		"CREATE INDEX idx_kubectl_sessions_reap",
		// Session commands table + FK CASCADE.
		"CREATE TABLE kubectl_session_commands",
		"session_id      UUID NOT NULL REFERENCES kubectl_sessions(id) ON DELETE CASCADE",
		"CREATE INDEX idx_kubectl_session_commands_session",
		// Hard cap of 4 hours on the row.
		"expires_at      TIMESTAMPTZ NOT NULL DEFAULT (now() + interval '4 hours')",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_KubectlSessions_DownContent(t *testing.T) {
	down := loadMigration065File(t, "065_kubectl_sessions.down.sql")

	for _, want := range []string{
		"DROP INDEX IF EXISTS idx_kubectl_session_commands_session",
		"DROP TABLE IF EXISTS kubectl_session_commands",
		"DROP INDEX IF EXISTS idx_kubectl_sessions_reap",
		"DROP INDEX IF EXISTS idx_kubectl_sessions_active",
		"DROP INDEX IF EXISTS idx_kubectl_sessions_user",
		"DROP TABLE IF EXISTS kubectl_sessions",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}

	// Indexes drop before their tables — symmetric rollback.
	posSessionsIdx := strings.Index(down, "idx_kubectl_sessions_user")
	posSessionsTbl := strings.Index(down, "DROP TABLE IF EXISTS kubectl_sessions")
	posCmdsIdx := strings.Index(down, "idx_kubectl_session_commands_session")
	posCmdsTbl := strings.Index(down, "DROP TABLE IF EXISTS kubectl_session_commands")
	if posSessionsIdx < 0 || posSessionsTbl < 0 || posCmdsIdx < 0 || posCmdsTbl < 0 {
		t.Fatalf("expected DROP statements missing")
	}
	if posSessionsTbl < posSessionsIdx {
		t.Errorf("kubectl_sessions table dropped before its indexes")
	}
	if posCmdsTbl < posCmdsIdx {
		t.Errorf("kubectl_session_commands table dropped before its index")
	}
}
