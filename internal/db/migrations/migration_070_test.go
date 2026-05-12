package migrations_test

// Static content test for migration 070_apiserver_allowlist.
//
// CI's helm-test path covers the full Postgres apply against an empty
// database; this static check is the fast-feedback lint that protects
// the structural invariants the handler + worker depend on:
//
//   - apiserver_allowlists has the cluster_id PRIMARY KEY (one row per
//     cluster — the handler relies on this on the PUT-upsert path).
//   - cidrs / effective_cidrs default to '[]' so a future ADD COLUMN
//     against a populated table doesn't break the JSONB array shape the
//     renderer iterates over.
//   - mode CHECK constraint enforces the three valid values; the handler
//     pre-validates but the DB is the backstop.
//   - sync_status CHECK constraint likewise enforces the four states.
//   - The snapshots table has the (cluster_id, captured_at DESC) index
//     the snapshots-list query depends on.
//   - The down file drops both tables in the right order so the FK
//     doesn't dangle.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration070File(t *testing.T, name string) string {
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

func TestMigration_ApiserverAllowlist_UpContent(t *testing.T) {
	up := loadMigration070File(t, "070_apiserver_allowlist.up.sql")

	for _, want := range []string{
		"CREATE TABLE apiserver_allowlists",
		"CREATE TABLE apiserver_allowlist_snapshots",
		"cluster_id      UUID PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE",
		"cidrs           JSONB NOT NULL DEFAULT '[]'",
		"mode            VARCHAR(16) NOT NULL DEFAULT 'monitor'",
		"detected_provider VARCHAR(32) NOT NULL DEFAULT 'unknown'",
		"sync_status      VARCHAR(16) NOT NULL DEFAULT 'pending'",
		"effective_cidrs  JSONB NOT NULL DEFAULT '[]'",
		"CONSTRAINT allowlist_mode_valid CHECK (mode IN ('enforce','monitor','disabled'))",
		"CONSTRAINT allowlist_status_valid CHECK (sync_status IN ('synced','drifting','pending','failed'))",
		// snapshots table
		"cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE",
		"drift           BOOLEAN NOT NULL DEFAULT false",
		"CREATE INDEX idx_allowlist_snapshots_cluster",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_ApiserverAllowlist_DownContent(t *testing.T) {
	down := loadMigration070File(t, "070_apiserver_allowlist.down.sql")
	for _, want := range []string{
		"DROP TABLE IF EXISTS apiserver_allowlist_snapshots",
		"DROP TABLE IF EXISTS apiserver_allowlists",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}
	// Child table must drop FIRST (its FK references apiserver_allowlists's
	// cluster — actually clusters — but ordering is still a backstop in
	// case the FK shape changes during a future refactor).
	posSnap := strings.Index(down, "DROP TABLE IF EXISTS apiserver_allowlist_snapshots")
	posParent := strings.Index(down, "DROP TABLE IF EXISTS apiserver_allowlists;")
	if posSnap < 0 || posParent < 0 {
		t.Fatalf("missing expected DROP statements")
	}
	if posSnap > posParent {
		t.Errorf("snapshots table dropped AFTER apiserver_allowlists; rollback order is unsafe")
	}
}
