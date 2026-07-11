package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// pendingSyncOp wires the recorder so processPendingOperations claims exactly
// one pending sync op and runs its Run closure with the given attempt count.
func pendingSyncOp(t *testing.T, rec *argoCDQueryRecorder, attemptCount int32) {
	t.Helper()
	id := uuid.New()
	payload := mustJSON(t, argocdOperationEnvelope{
		ApplicationID: rec.app.ID.String(),
		InstanceID:    rec.instance.ID.String(),
	})
	pending := sqlc.ArgocdOperation{
		ID:            id,
		OperationType: "sync",
		Status:        "pending",
		TargetType:    "application",
		TargetKey:     rec.app.ID.String(),
		Payload:       payload,
	}
	running := pending
	running.Status = "running"
	running.AttemptCount = attemptCount
	rec.pendingOps = []sqlc.ArgocdOperation{pending}
	rec.markRunning = running
}

// A transient submission failure (instance not healthy → ErrUnreachable-kind
// sentinel) must requeue the op, not permanently fail it.
func TestProcessPendingOperations_RequeuesTransientFailure(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec.instance.IsHealthy = false // executeSync returns the transient not-healthy sentinel
	pendingSyncOp(t, rec, 1)

	h.processPendingOperations(context.Background())

	if len(rec.requeued) != 1 {
		t.Fatalf("RequeueArgoCDOperation calls = %d, want 1 (transient failure requeued)", len(rec.requeued))
	}
	if len(rec.failed) != 0 {
		t.Fatalf("FailArgoCDOperationWithResult calls = %d, want 0 (transient must not fail terminally)", len(rec.failed))
	}
}

// An auth rejection (ErrUnauthorized-kind) is terminal: fail, do not requeue.
func TestProcessPendingOperations_FailsTerminalUnauthorized(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	pendingSyncOp(t, rec, 1)

	h.processPendingOperations(context.Background())

	if len(rec.failed) != 1 {
		t.Fatalf("FailArgoCDOperationWithResult calls = %d, want 1 (unauthorized is terminal)", len(rec.failed))
	}
	if len(rec.requeued) != 0 {
		t.Fatalf("RequeueArgoCDOperation calls = %d, want 0 (terminal must not requeue)", len(rec.requeued))
	}
}

// A transient failure that has already exhausted the submit-attempt cap is
// treated as terminal.
func TestProcessPendingOperations_FailsAfterAttemptCap(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec.instance.IsHealthy = false // transient sentinel
	pendingSyncOp(t, rec, maxArgoCDSubmitAttempts)

	h.processPendingOperations(context.Background())

	if len(rec.failed) != 1 {
		t.Fatalf("FailArgoCDOperationWithResult calls = %d, want 1 (attempt cap reached is terminal)", len(rec.failed))
	}
	if len(rec.requeued) != 0 {
		t.Fatalf("RequeueArgoCDOperation calls = %d, want 0 (capped attempts must not requeue)", len(rec.requeued))
	}
}
