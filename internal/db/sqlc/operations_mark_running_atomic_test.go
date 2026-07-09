package sqlc

import (
	"strings"
	"testing"
)

// pinAtomicClaimSQL asserts a Mark*OperationRunning statement uses the
// CORR-R01 atomic claim predicate (pending or stale running), matching
// MarkArgoCDOperationRunning. There is no live-Postgres unit harness in this
// package, so the generated statement string is the contract.
func pinAtomicClaimSQL(t *testing.T, name, sql string) {
	t.Helper()
	if !strings.Contains(sql, "status = 'pending'") {
		t.Errorf("%s must only claim rows that are still 'pending'; got:\n%s", name, sql)
	}
	if !strings.Contains(sql, "status = 'running'") || !strings.Contains(sql, "started_at") {
		t.Errorf("%s must permit re-claiming a stale 'running' lease; got:\n%s", name, sql)
	}
	whereIdx := strings.Index(sql, "WHERE id = $1")
	if whereIdx == -1 {
		t.Fatalf("%s: expected `WHERE id = $1` anchor; got:\n%s", name, sql)
	}
	tail := sql[whereIdx:]
	if !strings.Contains(tail, "AND") {
		t.Errorf("%s WHERE clause must AND a status precondition onto the id match; got:\n%s", name, sql)
	}
	if !strings.Contains(sql, "1 minute") && !strings.Contains(sql, "interval '1 minute'") {
		// Generated SQL keeps the interval literal from the query file.
		if !strings.Contains(strings.ToLower(sql), "minute") {
			t.Errorf("%s must include a 1-minute stale lease window; got:\n%s", name, sql)
		}
	}
}

func TestMarkToolOperationRunningIsAtomicClaim(t *testing.T) {
	pinAtomicClaimSQL(t, "MarkToolOperationRunning", markToolOperationRunning)
}

func TestMarkCatalogOperationRunningIsAtomicClaim(t *testing.T) {
	pinAtomicClaimSQL(t, "MarkCatalogOperationRunning", markCatalogOperationRunning)
}

func TestMarkLoggingOperationRunningIsAtomicClaim(t *testing.T) {
	pinAtomicClaimSQL(t, "MarkLoggingOperationRunning", markLoggingOperationRunning)
}

func TestMarkWorkloadOperationRunningIsAtomicClaim(t *testing.T) {
	pinAtomicClaimSQL(t, "MarkWorkloadOperationRunning", markWorkloadOperationRunning)
}

func TestMarkMonitoringOperationRunningIsAtomicClaim(t *testing.T) {
	pinAtomicClaimSQL(t, "MarkMonitoringOperationRunning", markMonitoringOperationRunning)
}

func TestUpdateBackupStartedIsCASClaim(t *testing.T) {
	sql := updateBackupStarted
	if !strings.Contains(sql, "status IN") && !strings.Contains(sql, "status =") {
		t.Errorf("UpdateBackupStarted must gate on claimable statuses; got:\n%s", sql)
	}
	if !strings.Contains(sql, "pending") {
		t.Errorf("UpdateBackupStarted must include pending; got:\n%s", sql)
	}
}

func TestUpdateRestoreOperationStartedIsCASClaim(t *testing.T) {
	sql := updateRestoreOperationStarted
	if !strings.Contains(sql, "pending") {
		t.Errorf("UpdateRestoreOperationStarted must include pending; got:\n%s", sql)
	}
}
