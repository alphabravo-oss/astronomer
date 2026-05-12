package migrations_test

// Static content test for migration 052_velero_snapshots.
//
// As with the other migration tests in this directory, we don't run
// the migration against Postgres here — the helm-test CI path covers
// that. This test guards against well-meaning future edits that would:
//
//   - Drop the FK to clusters(id) or users(id), which would let stale
//     rows linger after cluster decommission / user delete.
//   - Forget the partial WHERE index on phase IN ('InProgress','New'),
//     which is what makes the poller's sweep cheap.
//   - Drop the UNIQUE (cluster_id, name) on snapshot schedules — a
//     dup-name schedule was the original UX bug the constraint exists
//     to prevent.
//   - Reverse the DROP order in the .down.sql (snapshot must come
//     before restores because cluster_restores FKs snapshot_id).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration052File(t *testing.T, name string) string {
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

func TestMigration_VeleroSnapshots_UpContent(t *testing.T) {
	up := loadMigration052File(t, "052_velero_snapshots.up.sql")

	for _, want := range []string{
		"CREATE TABLE cluster_snapshots",
		"CREATE TABLE cluster_restores",
		"CREATE TABLE cluster_snapshot_schedules",
		"cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE",
		"velero_namespace VARCHAR(63)  NOT NULL DEFAULT 'velero'",
		"phase           VARCHAR(32) NOT NULL DEFAULT 'New'",
		// Partial index that makes the poller cheap.
		"WHERE phase IN ('InProgress','New')",
		// Per-target lookup for restores.
		"CREATE INDEX idx_cluster_restores_cluster ON cluster_restores (target_cluster_id, created_at DESC)",
		// Dup-name guard on schedules.
		"UNIQUE (cluster_id, name)",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_VeleroSnapshots_DownContent(t *testing.T) {
	down := loadMigration052File(t, "052_velero_snapshots.down.sql")

	for _, want := range []string{
		"DROP TABLE IF EXISTS cluster_snapshot_schedules",
		"DROP TABLE IF EXISTS cluster_restores",
		"DROP TABLE IF EXISTS cluster_snapshots",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down missing %q", want)
		}
	}

	// Drop order matters: cluster_restores FK-references cluster_snapshots
	// via the snapshot_id column, so the restores table must come off
	// first. (CASCADE on the parent drop would also handle it, but
	// we keep the order explicit so a future rollback reading the
	// .down.sql still sees the dependency direction.)
	posRestores := strings.Index(down, "DROP TABLE IF EXISTS cluster_restores")
	posSnapshots := strings.Index(down, "DROP TABLE IF EXISTS cluster_snapshots")
	if posRestores < 0 || posSnapshots < 0 {
		t.Fatalf("DROP statements missing")
	}
	if posSnapshots < posRestores {
		t.Errorf("cluster_snapshots dropped before cluster_restores; FK rollback would fail")
	}
}
