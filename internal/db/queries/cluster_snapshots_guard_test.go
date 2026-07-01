package queries_test

// Content guard for the ListEnabledSnapshotSchedules due-query. The snapshot
// dispatcher iterates this set and creates Velero backup jobs for every due
// schedule; it MUST skip decommissioned clusters. The decommission reconciler
// tombstones (does not hard-delete) the cluster row, so ON DELETE CASCADE
// never fires and an orphaned schedule would otherwise keep firing snapshots
// against a dead cluster forever (finding F03). The query therefore JOINs
// clusters and requires decommissioned_at IS NULL.
//
// We test the SQL file content directly rather than running it against a
// database; the sibling clusters_guard_test.go / migration tests follow the
// same pattern.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListEnabledSnapshotSchedules_GuardsDecommissioned(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "cluster_snapshots.sql"))
	if err != nil {
		t.Fatalf("read cluster_snapshots.sql: %v", err)
	}
	sql := string(b)
	const header = "-- name: ListEnabledSnapshotSchedules :many"
	i := strings.Index(sql, header)
	if i < 0 {
		t.Fatalf("ListEnabledSnapshotSchedules query missing from cluster_snapshots.sql")
	}
	rest := sql[i+len(header):]
	if next := strings.Index(rest, "-- name:"); next >= 0 {
		rest = rest[:next]
	}
	if !strings.Contains(rest, "clusters") {
		t.Errorf("ListEnabledSnapshotSchedules must JOIN clusters to filter decommissioned rows; body was:\n%s", rest)
	}
	if !strings.Contains(rest, "decommissioned_at IS NULL") {
		t.Errorf("ListEnabledSnapshotSchedules must guard on decommissioned_at IS NULL; body was:\n%s", rest)
	}
}
