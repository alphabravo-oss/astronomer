package migrations_test

// Static content test for migration 054_sso_sessions.
//
// As with the other migration_*_test.go siblings, we DO NOT run the
// migration against Postgres — the CI helm-test path covers that via
// the migrate-job container. What we check here is the SHAPE of the
// SQL, so an unrelated future edit can't quietly:
//
//   - Drop the FK ON DELETE CASCADE on user_id. Without it, deleting a
//     user would either fail (FK with RESTRICT) or leave dangling
//     sso_sessions rows referencing a nonexistent user.
//   - Forget the partial expires-at index. Without it, the nightly
//     retention purge degrades into a seq-scan on a (potentially)
//     large table.
//   - Forget the user_id index, which the admin force-logout path
//     uses to enumerate a user's active upstream sessions for
//     back-channel logout.
//   - Drop the encrypted-at-rest comment trail / column. The
//     id_token is bearer-equivalent; storing it plaintext would
//     turn a DB read leak into a logout-forging vector.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration054File(t *testing.T, name string) string {
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

func TestMigration_SSOSessions_UpContent(t *testing.T) {
	up := loadMigration054File(t, "054_sso_sessions.up.sql")

	for _, want := range []string{
		"CREATE TABLE sso_sessions",
		// JTI is the join column — must be the PK so each Astronomer
		// session maps to at most one upstream session row.
		"jti                         VARCHAR(64) PRIMARY KEY",
		// FK + CASCADE so a user deletion cleans up their sessions.
		"REFERENCES users(id) ON DELETE CASCADE",
		// id_token is encrypted at rest.
		"upstream_id_token_encrypted TEXT        NOT NULL",
		// end_session_endpoint cached on the row to skip discovery on
		// the Logout hot path.
		"end_session_endpoint        TEXT        NOT NULL DEFAULT ''",
		// expires_at drives the retention GC.
		"expires_at                  TIMESTAMPTZ NOT NULL",
		// Required indexes.
		"CREATE INDEX idx_sso_sessions_user",
		"CREATE INDEX idx_sso_sessions_expires",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_SSOSessions_DownContent(t *testing.T) {
	down := loadMigration054File(t, "054_sso_sessions.down.sql")

	for _, want := range []string{
		"DROP INDEX IF EXISTS idx_sso_sessions_expires",
		"DROP INDEX IF EXISTS idx_sso_sessions_user",
		"DROP TABLE IF EXISTS sso_sessions",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}

	// Order: indexes drop before the table. Not strictly required
	// (DROP TABLE drops dependent indexes implicitly) but the explicit
	// form keeps the rollback symmetric with the up migration.
	posExpIdx := strings.Index(down, "idx_sso_sessions_expires")
	posUserIdx := strings.Index(down, "idx_sso_sessions_user")
	posTable := strings.Index(down, "DROP TABLE IF EXISTS sso_sessions")
	if posExpIdx < 0 || posUserIdx < 0 || posTable < 0 {
		t.Fatalf("missing one of the expected DROP statements")
	}
	if posTable < posExpIdx || posTable < posUserIdx {
		t.Errorf("sso_sessions table dropped before its indexes; rollback would log warnings")
	}
}
