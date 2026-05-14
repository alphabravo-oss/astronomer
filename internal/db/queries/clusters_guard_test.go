package queries_test

// Content guard for the UpdateClusterStatus query. The query must
// refuse to overwrite already-tombstoned cluster rows so that the
// periodic health-check and metrics-publisher sweepers can't race
// against the cluster_decommission reconciler and re-introduce the
// "ghost row" drift backfilled by migration 088.
//
// We test the SQL file content directly rather than running it
// against a database; the migration tests follow the same pattern.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateClusterStatus_GuardsDecommissioned(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "clusters.sql"))
	if err != nil {
		t.Fatalf("read clusters.sql: %v", err)
	}
	sql := string(b)
	// Locate the named-block by header; everything until the next
	// "-- name:" header is the query body.
	const header = "-- name: UpdateClusterStatus :exec"
	i := strings.Index(sql, header)
	if i < 0 {
		t.Fatalf("UpdateClusterStatus query missing from clusters.sql")
	}
	rest := sql[i+len(header):]
	if next := strings.Index(rest, "-- name:"); next >= 0 {
		rest = rest[:next]
	}
	if !strings.Contains(rest, "decommissioned_at IS NULL") {
		t.Errorf("UpdateClusterStatus must guard on decommissioned_at IS NULL; body was:\n%s", rest)
	}
}
