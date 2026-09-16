package sqlc

import (
	"strings"
	"testing"
)

// TestRecordAgentHeartbeatKeepsLastGoodInventory pins the L11 keep-last-good
// shape of the atomic RecordAgentHeartbeat query. The guard lives entirely in SQL
// (a degraded/minimal beat sends empty/zero inventory and must NOT clobber the
// prior columns), and there is no live-Postgres unit harness in this repo, so
// this test locks the generated statement so the guard cannot silently
// regress: narrow liveness must always advance, but each inventory column must
// fall back to its existing value when the incoming arg is empty/zero.
func TestRecordAgentHeartbeatKeepsLastGoodInventory(t *testing.T) {
	sql := recordAgentHeartbeat

	if !strings.Contains(sql, "INSERT INTO cluster_liveness") || !strings.Contains(sql, "last_heartbeat = EXCLUDED.last_heartbeat") {
		t.Fatalf("RecordAgentHeartbeat must advance narrow liveness:\n%s", sql)
	}
	inventorySQL := sql[strings.Index(sql, "UPDATE clusters"):]
	if strings.Contains(inventorySQL, "SET last_heartbeat") {
		t.Fatalf("RecordAgentHeartbeat must not write clusters.last_heartbeat:\n%s", sql)
	}
	if !strings.Contains(sql, "UPDATE agent_connections") || !strings.Contains(sql, "INSERT INTO cluster_health_statuses") {
		t.Fatalf("RecordAgentHeartbeat must consolidate connection and health persistence:\n%s", sql)
	}

	// Keep-last-good for text inventory columns: empty arg preserves prior value.
	for _, col := range []string{"agent_version", "kubernetes_version", "distribution"} {
		want := col + " = COALESCE(NULLIF("
		if !strings.Contains(sql, want) {
			t.Fatalf("RecordAgentHeartbeat must keep-last-good %q via COALESCE/NULLIF:\n%s", col, sql)
		}
		// The fallback target must be the column itself (prior value).
		if !strings.Contains(sql, "), "+col+")") && !strings.Contains(sql, "), "+col+")\n") {
			t.Fatalf("RecordAgentHeartbeat %q must fall back to its own column:\n%s", col, sql)
		}
	}

	// Keep-last-good for node_count: only a positive count overwrites; zero
	// (a failed list_nodes collect) preserves the prior count.
	if !strings.Contains(sql, "node_count = CASE WHEN") || !strings.Contains(sql, "> 0 THEN") {
		t.Fatalf("RecordAgentHeartbeat must keep-last-good node_count via a >0 guard:\n%s", sql)
	}
	if !strings.Contains(sql, "ELSE node_count END") {
		t.Fatalf("RecordAgentHeartbeat node_count must fall back to prior value:\n%s", sql)
	}
}
