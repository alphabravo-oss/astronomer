package sqlc

import (
	"strings"
	"testing"
)

// TestMarkArgoCDOperationRunningIsAtomicClaim pins the atomic-claim precondition
// on MarkArgoCDOperationRunning. There is no live-Postgres unit harness in this
// package, so the test locks the generated statement string so the fix cannot
// silently regress.
//
// Bug: the UPDATE previously matched on `WHERE id = $1` alone with no status
// precondition. Under an HA deployment (server.replicaCount>1) two workers can
// ListPendingArgoCDOperations the same pending sync op and both
// MarkArgoCDOperationRunning succeed, firing two upstream
// `POST /api/v1/applications/{name}/sync` calls for one requested op. Gating the
// UPDATE on the row still being claimable (pending, or a stale running lease)
// means the second worker's UPDATE matches zero rows -> pgx.ErrNoRows ->
// claimLatestOperations skips it.
func TestMarkArgoCDOperationRunningIsAtomicClaim(t *testing.T) {
	sql := markArgoCDOperationRunning

	// The claim must be conditional on a claimable status, not just the id.
	if !strings.Contains(sql, "status = 'pending'") {
		t.Errorf("MarkArgoCDOperationRunning must only claim rows that are still 'pending'; got:\n%s", sql)
	}
	// Stale running leases must still be re-claimable so a crashed worker's op
	// is recovered by the reconciler.
	if !strings.Contains(sql, "status = 'running'") || !strings.Contains(sql, "started_at") {
		t.Errorf("MarkArgoCDOperationRunning must permit re-claiming a stale 'running' lease; got:\n%s", sql)
	}
	// Guard against a naive `WHERE id = $1` with no status precondition.
	whereIdx := strings.Index(sql, "WHERE id = $1")
	if whereIdx == -1 {
		t.Fatalf("expected `WHERE id = $1` anchor in statement; got:\n%s", sql)
	}
	tail := sql[whereIdx:]
	if !strings.Contains(tail, "AND") {
		t.Errorf("MarkArgoCDOperationRunning WHERE clause must AND a status precondition onto the id match; got:\n%s", sql)
	}
}
