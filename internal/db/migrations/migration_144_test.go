package migrations_test

import (
	"os"
	"strings"
	"testing"
)

// Migration 144 gives helm_repositories somewhere to record a failed sync so
// the newly-isolating catalog:sync sweep has a place to put per-repository
// failures. It must be purely additive: an operator upgrading with a broken
// repo must not have its history rewritten, and last_synced_at must keep its
// meaning ("last SUCCESSFUL ingest") or a permanently failing repository would
// read as permanently fresh.
func TestMigration144HelmRepositorySyncStatus(t *testing.T) {
	up, err := os.ReadFile("144_helm_repository_sync_status.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"ADD COLUMN IF NOT EXISTS last_sync_error TEXT NOT NULL DEFAULT ''",
		"ADD COLUMN IF NOT EXISTS last_sync_attempted_at TIMESTAMPTZ",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("up migration missing %q", required)
		}
	}
	// Additive DDL only. An UPDATE here would either fabricate a failure
	// against a healthy repository or erase one an operator needs to see, and
	// a backfill of last_sync_attempted_at from last_synced_at would assert an
	// attempt history the database never observed.
	//
	// Needles must be UPPERCASE: the haystack is ToUpper'd, so a mixed-case
	// needle can never match and the check silently passes forever.
	for _, forbidden := range []string{
		"UPDATE HELM_REPOSITORIES",
		"DELETE FROM",
		"DROP COLUMN",
		"ALTER COLUMN",
	} {
		if strings.Contains(strings.ToUpper(string(up)), forbidden) {
			t.Fatalf("up migration must be additive only, found %q", forbidden)
		}
	}

	down, err := os.ReadFile("144_helm_repository_sync_status.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"DROP COLUMN IF EXISTS last_sync_attempted_at",
		"DROP COLUMN IF EXISTS last_sync_error",
	} {
		if !strings.Contains(string(down), required) {
			t.Fatalf("down migration missing %q", required)
		}
	}
}
