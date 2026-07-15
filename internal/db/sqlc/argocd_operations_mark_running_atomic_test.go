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
// UPDATE on the row still being pending means the second worker's UPDATE
// matches zero rows -> pgx.ErrNoRows -> claimLatestOperations skips it.
// Running Argo operations are asynchronous and must only be recovered by the
// poll claim; redispatching them restarts upstream hooks and duplicates work.
func TestMarkArgoCDOperationRunningIsAtomicClaim(t *testing.T) {
	sql := markArgoCDOperationRunning

	// The claim must be conditional on a claimable status, not just the id.
	if !strings.Contains(sql, "status = 'pending'") {
		t.Errorf("MarkArgoCDOperationRunning must only claim rows that are still 'pending'; got:\n%s", sql)
	}
	if strings.Contains(sql[strings.Index(sql, "WHERE id = $1"):], "status = 'running'") {
		t.Errorf("MarkArgoCDOperationRunning must never redispatch an asynchronous running operation; got:\n%s", sql)
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

func TestArgoCDOperationPollingUsesHADurableClaimAndTerminalCAS(t *testing.T) {
	if strings.Contains(listPendingArgoCDOperations, "status IN") || strings.Contains(listPendingArgoCDOperations, "'running'") {
		t.Errorf("pending query must not return running operations:\n%s", listPendingArgoCDOperations)
	}
	poll := claimRunningArgoCDOperationsForPoll
	for _, required := range []string{"FOR UPDATE SKIP LOCKED", "last_polled_at", "poll_attempts", "status = 'running'"} {
		if !strings.Contains(poll, required) {
			t.Errorf("poll claim missing %q:\n%s", required, poll)
		}
	}
	for name, statement := range map[string]string{
		"progress": updateArgoCDOperationProgress,
		"complete": completeArgoCDOperationWithResult,
		"fail":     failArgoCDOperationWithResult,
	} {
		where := strings.Index(statement, "WHERE id = $1")
		if where < 0 || !strings.Contains(statement[where:], "status = 'running'") {
			t.Errorf("%s fold must CAS on running state:\n%s", name, statement)
		}
	}
}
