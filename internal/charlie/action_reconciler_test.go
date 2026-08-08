package charlie

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type reconcileQueriesFake struct {
	receipt     sqlc.CharlieActionReceipt
	claimed     bool
	claimCalls  int
	transition  sqlc.TransitionCharlieActionReceiptParams
	transitionN int
}

func (f *reconcileQueriesFake) ClaimCharlieAmbiguousReceipt(_ context.Context, _ sqlc.ClaimCharlieAmbiguousReceiptParams) (sqlc.CharlieActionReceipt, error) {
	f.claimCalls++
	if f.claimed {
		return sqlc.CharlieActionReceipt{}, pgx.ErrNoRows
	}
	f.claimed = true
	if f.receipt.State != "claimed" {
		f.receipt.State = "verifying"
	}
	return f.receipt, nil
}

func (f *reconcileQueriesFake) TransitionCharlieActionReceipt(_ context.Context, arg sqlc.TransitionCharlieActionReceiptParams) (sqlc.CharlieActionReceipt, error) {
	f.transitionN++
	f.transition = arg
	f.receipt.State = arg.NextState
	f.receipt.ResultStatus = arg.ResultStatus
	f.receipt.ResultDigest = arg.ResultDigest
	f.receipt.ResultEncrypted = arg.ResultEncrypted
	return f.receipt, nil
}

type reconcileExecutorFake struct {
	verified        bool
	verifyCalls     int
	executeCalls    int
	verifyArguments map[string]json.RawMessage
}

func (f *reconcileExecutorFake) Execute(context.Context, CapabilityDescriptor, map[string]json.RawMessage) (json.RawMessage, error) {
	f.executeCalls++
	return nil, nil
}
func (f *reconcileExecutorFake) Verify(_ context.Context, _ CapabilityDescriptor, arguments map[string]json.RawMessage, _ json.RawMessage) (bool, error) {
	f.verifyCalls++
	f.verifyArguments = arguments
	return f.verified, nil
}

type reconcileAuditFake struct {
	outcome string
	calls   int
}

func (f *reconcileAuditFake) RecordReceiptReconciliation(_ context.Context, _ sqlc.CharlieActionReceipt, outcome string) error {
	f.calls++
	f.outcome = outcome
	return nil
}

func reconcilerFixture(t *testing.T, verified bool, age time.Duration) (*AmbiguousReceiptReconciler, *reconcileQueriesFake, *reconcileExecutorFake, *reconcileAuditFake) {
	t.Helper()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	arguments := json.RawMessage(`{"resource_id":"resource-a","task_id":"task-a","operation_id":"model-selected-label"}`)
	canonical, _, err := canonicalArguments(arguments)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := receiptTestCipher{}.Encrypt(string(arguments))
	queries := &reconcileQueriesFake{receipt: sqlc.CharlieActionReceipt{
		ID: uuid.New(), CharlieActionID: "action-a", Capability: "astronomer.queue.retry_task", Effect: "write",
		ArgumentDigest: digestBytes(canonical), ArgumentsEncrypted: sealed, FencingEpoch: 7, State: "dispatched",
		AuditCorrelationID: uuid.New(), CreatedAt: now.Add(-age),
		DispatchedAt: pgtype.Timestamptz{Time: now.Add(-age), Valid: true}, Attempt: 1,
	}}
	executor := &reconcileExecutorFake{verified: verified}
	auditor := &reconcileAuditFake{}
	reconciler, err := NewAmbiguousReceiptReconciler(queries, receiptTestCipher{}, executor, auditor, "server-a", func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	reconciler.now = func() time.Time { return now }
	return reconciler, queries, executor, auditor
}

func TestAmbiguousReceiptReconcilerVerifiesWithoutRedispatch(t *testing.T) {
	reconciler, queries, executor, auditor := reconcilerFixture(t, true, time.Minute)
	if err := reconciler.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if executor.executeCalls != 0 || executor.verifyCalls != 1 || queries.transition.NextState != "succeeded" || auditor.outcome != "succeeded" {
		t.Fatalf("unsafe reconciliation: execute=%d verify=%d transition=%+v audit=%+v", executor.executeCalls, executor.verifyCalls, queries.transition, auditor)
	}
	var operationID string
	if err := json.Unmarshal(executor.verifyArguments["operation_id"], &operationID); err != nil || operationID != queries.receipt.CharlieActionID {
		t.Fatalf("reconciliation used untrusted operation ID %q: %v", operationID, err)
	}
	plain, err := receiptTestCipher{}.Decrypt(queries.transition.ResultEncrypted)
	if err != nil || !json.Valid([]byte(plain)) || queries.transition.ResultDigest != digestBytes([]byte(plain)) {
		t.Fatal("reconciled terminal result is not encrypted and integrity-bound")
	}
}

func TestAmbiguousReceiptReconcilerKeepsRecentUnknownOutcomeAmbiguous(t *testing.T) {
	reconciler, queries, executor, auditor := reconcilerFixture(t, false, time.Minute)
	if err := reconciler.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if executor.executeCalls != 0 || queries.transition.NextState != "ambiguous" || auditor.outcome != "ambiguous" {
		t.Fatalf("recent unverified receipt was not held ambiguous: %+v", queries.transition)
	}
}

func TestAmbiguousReceiptReconcilerTerminatesExpiredUnverifiedOutcome(t *testing.T) {
	reconciler, queries, executor, auditor := reconcilerFixture(t, false, ambiguousReceiptVerificationLimit+time.Minute)
	if err := reconciler.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if executor.executeCalls != 0 || queries.transition.NextState != "failed" || auditor.outcome != "failed" {
		t.Fatalf("expired unverified receipt did not terminate safely: %+v", queries.transition)
	}
}

func TestAmbiguousReceiptReconcilerBlocksExpiredPreDispatchClaimWithoutVerification(t *testing.T) {
	reconciler, queries, executor, auditor := reconcilerFixture(t, true, time.Minute)
	queries.receipt.State = "claimed"
	queries.receipt.DispatchedAt = pgtype.Timestamptz{}

	if err := reconciler.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if executor.executeCalls != 0 || executor.verifyCalls != 0 || queries.transition.NextState != "blocked" ||
		queries.transition.ExpectedState != "claimed" || queries.transition.ResultStatus != "blocked" ||
		auditor.outcome != "blocked" {
		t.Fatalf("stale pre-dispatch claim was not terminally blocked: execute=%d verify=%d transition=%+v audit=%+v",
			executor.executeCalls, executor.verifyCalls, queries.transition, auditor)
	}
	plain, err := receiptTestCipher{}.Decrypt(queries.transition.ResultEncrypted)
	if err != nil {
		t.Fatal(err)
	}
	var result ActionResult
	if json.Unmarshal([]byte(plain), &result) != nil || result.Allowed || result.Code != DeniedAmbiguousPriorAttempt ||
		result.State != "blocked" {
		t.Fatalf("stale pre-dispatch replay result = %+v", result)
	}
}

func TestAmbiguousReceiptReconcilerIsDormantWhenCharlieInactive(t *testing.T) {
	_, queries, executor, auditor := reconcilerFixture(t, true, time.Minute)
	reconciler, err := NewAmbiguousReceiptReconciler(queries, receiptTestCipher{}, executor, auditor, "server-a", func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.RunOnce(context.Background()); err != nil || queries.claimCalls != 0 || executor.verifyCalls != 0 {
		t.Fatal("inactive Charlie reconciler touched durable state or product APIs")
	}
}
