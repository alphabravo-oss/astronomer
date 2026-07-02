package sqlc

import (
	"strings"
	"testing"
)

// TestTaskOutboxUpsertsResetAttemptCount guards the fix for the dead-letter
// re-open bug: when a task_outbox row is re-triggered (UpsertTaskOutbox /
// *_with_task_outbox ON CONFLICT), a NON-delivered row must reset
// attempt_count to 0 so it gets a fresh delivery budget. Without the reset a
// row that previously climbed to max_delivery_attempts (dead) re-opens to
// 'pending' with its exhausted count intact and dead-letters again on the
// first transient error — silently dropping a recoverable delivery.
//
// A 'delivered' row must PRESERVE its attempt_count (idempotent no-op
// re-open), so we assert the CASE keys on task_outbox.status='delivered'.
func TestTaskOutboxUpsertsResetAttemptCount(t *testing.T) {
	// Every re-open path (base upsert + all *_with_task_outbox CTEs).
	consts := map[string]string{
		"upsertTaskOutbox": upsertTaskOutbox,
		"upsertCloudCredentialMaterializationWithTaskOutbox": upsertCloudCredentialMaterializationWithTaskOutbox,
		"deleteCloudCredentialMaterializationWithTaskOutbox": deleteCloudCredentialMaterializationWithTaskOutbox,
		"createClusterDecommissionWithTaskOutbox":            createClusterDecommissionWithTaskOutbox,
		"deleteClusterRegistryConfigByIDWithTaskOutbox":      deleteClusterRegistryConfigByIDWithTaskOutbox,
		"updateClusterRegistrationStepWithTaskOutbox":        updateClusterRegistrationStepWithTaskOutbox,
		"upsertClusterTemplateApplicationWithTaskOutbox":     upsertClusterTemplateApplicationWithTaskOutbox,
	}
	const wantReset = "attempt_count"
	const wantDeliveredGuard = "THEN task_outbox.attempt_count ELSE 0 END"

	for name, sql := range consts {
		// Only branches that actually re-open a conflicting row need the
		// reset. Every listed const has an ON CONFLICT ... DO UPDATE that
		// re-opens the outbox row, so all must carry it.
		if !strings.Contains(sql, "ON CONFLICT") {
			t.Fatalf("%s: expected an ON CONFLICT re-open branch", name)
		}
		if !strings.Contains(sql, wantReset) || !strings.Contains(sql, wantDeliveredGuard) {
			t.Errorf("%s: ON CONFLICT branch must reset attempt_count for non-delivered rows "+
				"(want %q keyed on delivered-status), got:\n%s", name, wantDeliveredGuard, sql)
		}
		// Guard against a blanket reset that would also wipe a delivered
		// row's history: the reset must be conditional on 'delivered'.
		if strings.Contains(sql, "attempt_count         = 0,") || strings.Contains(sql, "attempt_count = 0,") {
			t.Errorf("%s: attempt_count reset must be conditional on task_outbox.status='delivered', not unconditional", name)
		}
	}
}
