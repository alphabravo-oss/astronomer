package queries_test

// Content guard for the UpdateClusterStatusOnHeartbeat query (H-02). The
// health-check sweep computes a status from a full-fleet snapshot, so the write
// MUST atomically re-check the same 2m liveness window: 'active' only when the
// heartbeat is still fresh, 'disconnected' only when it is still stale/absent.
// Without both clauses a cluster that reconnects mid-sweep can be clobbered back
// to 'disconnected' for a cycle. We assert the SQL text directly, mirroring
// clusters_guard_test.go.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateClusterStatusOnHeartbeat_GuardsHeartbeatWindow(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "clusters.sql"))
	if err != nil {
		t.Fatalf("read clusters.sql: %v", err)
	}
	sql := string(b)
	const header = "-- name: UpdateClusterStatusOnHeartbeat :execrows"
	i := strings.Index(sql, header)
	if i < 0 {
		t.Fatalf("UpdateClusterStatusOnHeartbeat query missing from clusters.sql")
	}
	rest := sql[i+len(header):]
	if next := strings.Index(rest, "\n-- name:"); next >= 0 {
		rest = rest[:next]
	}
	// Still guards the tombstone invariant.
	if !strings.Contains(rest, "decommissioned_at IS NULL") {
		t.Errorf("must keep the decommissioned_at IS NULL guard; body:\n%s", rest)
	}
	// 'active' requires a fresh heartbeat.
	if !strings.Contains(rest, "'active'") ||
		!strings.Contains(rest, "last_heartbeat >= now() - interval '2 minutes'") {
		t.Errorf("'active' branch must require a fresh heartbeat; body:\n%s", rest)
	}
	// 'disconnected' requires a stale/absent heartbeat.
	if !strings.Contains(rest, "'disconnected'") ||
		!strings.Contains(rest, "last_heartbeat < now() - interval '2 minutes'") ||
		!strings.Contains(rest, "last_heartbeat IS NULL") {
		t.Errorf("'disconnected' branch must require a stale/absent heartbeat; body:\n%s", rest)
	}
}
