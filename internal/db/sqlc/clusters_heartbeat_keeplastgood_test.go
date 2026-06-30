package sqlc

import (
	"strings"
	"testing"
)

// TestUpdateClusterHeartbeatKeepsLastGoodInventory pins the L11 keep-last-good
// shape of the UpdateClusterHeartbeat query. The guard lives entirely in SQL
// (a degraded/minimal beat sends empty/zero inventory and must NOT clobber the
// prior columns), and there is no live-Postgres unit harness in this repo, so
// this test locks the generated statement so the guard cannot silently
// regress: last_heartbeat must always advance, but each inventory column must
// fall back to its existing value when the incoming arg is empty/zero.
func TestUpdateClusterHeartbeatKeepsLastGoodInventory(t *testing.T) {
	sql := updateClusterHeartbeat

	// Liveness always advances, decoupled from inventory (H11).
	if !strings.Contains(sql, "last_heartbeat = now()") {
		t.Fatalf("UpdateClusterHeartbeat must unconditionally advance last_heartbeat:\n%s", sql)
	}

	// Keep-last-good for text inventory columns: empty arg preserves prior value.
	for _, col := range []string{"agent_version", "kubernetes_version", "distribution"} {
		want := col + " = COALESCE(NULLIF("
		if !strings.Contains(sql, want) {
			t.Fatalf("UpdateClusterHeartbeat must keep-last-good %q via COALESCE/NULLIF:\n%s", col, sql)
		}
		// The fallback target must be the column itself (prior value).
		if !strings.Contains(sql, "), "+col+")") && !strings.Contains(sql, "), "+col+")\n") {
			t.Fatalf("UpdateClusterHeartbeat %q must fall back to its own column:\n%s", col, sql)
		}
	}

	// Keep-last-good for node_count: only a positive count overwrites; zero
	// (a failed list_nodes collect) preserves the prior count.
	if !strings.Contains(sql, "node_count = CASE WHEN") || !strings.Contains(sql, "> 0 THEN") {
		t.Fatalf("UpdateClusterHeartbeat must keep-last-good node_count via a >0 guard:\n%s", sql)
	}
	if !strings.Contains(sql, "ELSE node_count END") {
		t.Fatalf("UpdateClusterHeartbeat node_count must fall back to prior value:\n%s", sql)
	}
}
