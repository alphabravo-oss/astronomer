package sqlc

import (
	"strings"
	"testing"
)

func TestGatekeeperConstraintLifecycleQueriesPreserveGenerationOrdering(t *testing.T) {
	if !strings.Contains(listAuthoredConstraintsForCluster, "NOT (desired_state = 'absent' AND sync_status = 'synced')") {
		t.Fatalf("list must hide converged deletion tombstones while retaining pending/failed deletions:\n%s", listAuthoredConstraintsForCluster)
	}
	if !strings.Contains(getAuthoredConstraintByNameForUpdate, "FOR UPDATE") {
		t.Fatalf("delete decision must lock the authored row:\n%s", getAuthoredConstraintByNameForUpdate)
	}
	for _, clause := range []string{
		"desired_state = 'present'",
		"sync_status = 'pending'",
		"generation = authored_constraints.generation + 1",
		"last_error = ''",
	} {
		if !strings.Contains(upsertAuthoredConstraint, clause) {
			t.Errorf("upsert missing %q:\n%s", clause, upsertAuthoredConstraint)
		}
	}
	for _, clause := range []string{
		"desired_state = 'absent'",
		"sync_status = 'pending'",
		"generation = generation + 1",
	} {
		if !strings.Contains(markAuthoredConstraintDeleted, clause) {
			t.Errorf("delete intent missing %q:\n%s", clause, markAuthoredConstraintDeleted)
		}
	}
	// The generated parameter order follows first use in the UPDATE fields;
	// observed_generation is $2 and must also fence the target generation.
	if !strings.Contains(markAuthoredConstraintReconcileResult, "generation = $2") {
		t.Fatalf("worker outcome must be generation-conditional:\n%s", markAuthoredConstraintReconcileResult)
	}
	for _, clause := range []string{
		"sync_status = 'pending'",
		"sync_status = 'failed'",
		"COALESCE(last_reconciled_at, updated_at) <= now() - interval '5 minutes'",
	} {
		if !strings.Contains(listRecoverableAuthoredConstraints, clause) {
			t.Errorf("repair sweep missing %q:\n%s", clause, listRecoverableAuthoredConstraints)
		}
	}
}
