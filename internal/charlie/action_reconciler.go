package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

const (
	MaxAmbiguousReceiptReconcileBatch = 100
	ambiguousReceiptLease             = 45 * time.Second
	ambiguousReceiptVerificationLimit = 15 * time.Minute
)

type ambiguousReceiptQueries interface {
	ClaimCharlieAmbiguousReceipt(context.Context, sqlc.ClaimCharlieAmbiguousReceiptParams) (sqlc.CharlieActionReceipt, error)
	TransitionCharlieActionReceipt(context.Context, sqlc.TransitionCharlieActionReceiptParams) (sqlc.CharlieActionReceipt, error)
}

type ReceiptReconcileAudit interface {
	RecordReceiptReconciliation(context.Context, sqlc.CharlieActionReceipt, string) error
}

// AmbiguousReceiptReconciler never repeats a mutation. After a process crash
// it claims an expired receipt lease and invokes only the capability's declared
// read-only postcondition. A confirmed postcondition converges to succeeded;
// an unconfirmed postcondition remains ambiguous until a bounded deadline,
// after which it becomes failed rather than being blindly dispatched again.
type AmbiguousReceiptReconciler struct {
	queries    ambiguousReceiptQueries
	cipher     actionReceiptCipher
	executor   CapabilityExecutor
	auditor    ReceiptReconcileAudit
	leaseOwner string
	active     func() bool
	now        func() time.Time
}

func NewAmbiguousReceiptReconciler(queries ambiguousReceiptQueries, cipher actionReceiptCipher, executor CapabilityExecutor, auditor ReceiptReconcileAudit, leaseOwner string, active func() bool) (*AmbiguousReceiptReconciler, error) {
	if queries == nil || cipher == nil || executor == nil || auditor == nil || leaseOwner == "" || len(leaseOwner) > 128 || active == nil {
		return nil, fmt.Errorf("Charlie ambiguous receipt reconciliation requires durable state, encryption, verification, audit, and activation")
	}
	return &AmbiguousReceiptReconciler{queries: queries, cipher: cipher, executor: executor, auditor: auditor, leaseOwner: leaseOwner, active: active, now: time.Now}, nil
}

func (r *AmbiguousReceiptReconciler) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if r.active() {
			_ = r.RunOnce(ctx)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *AmbiguousReceiptReconciler) RunOnce(ctx context.Context) error {
	if !r.active() {
		return nil
	}
	for count := 0; count < MaxAmbiguousReceiptReconcileBatch; count++ {
		now := r.now().UTC()
		receipt, err := r.queries.ClaimCharlieAmbiguousReceipt(ctx, sqlc.ClaimCharlieAmbiguousReceiptParams{
			LeaseOwner: r.leaseOwner, LeaseExpiresAt: now.Add(ambiguousReceiptLease),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim ambiguous Charlie receipt: %w", err)
		}
		if err := r.reconcile(ctx, receipt, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *AmbiguousReceiptReconciler) reconcile(ctx context.Context, receipt sqlc.CharlieActionReceipt, now time.Time) error {
	// A claimed receipt has not crossed the durable dispatched transition, and
	// the action guard never invokes an adapter before that transition commits.
	// After its lease expires, terminally block the abandoned pre-dispatch claim
	// without running a postcondition or retrying the side effect. This makes an
	// exact replay safe while requiring a new action/approval for later work.
	if receipt.State == "claimed" {
		return r.finish(ctx, receipt, "blocked", ActionResult{
			Allowed: false, Code: DeniedAmbiguousPriorAttempt, State: "blocked", Verified: false,
		})
	}
	descriptor, found := capabilityByName(receipt.Capability)
	arguments, err := r.decryptArguments(receipt, descriptor, found)
	if err != nil {
		return r.finish(ctx, receipt, "failed", ActionResult{Allowed: true, State: "failed", Verified: false})
	}
	timeout := time.Duration(descriptor.TimeoutSeconds) * time.Second
	verifyCtx, cancel := context.WithTimeout(ctx, timeout)
	verified, verifyErr := r.executor.Verify(verifyCtx, descriptor, arguments, nil)
	cancel()
	if verifyErr == nil && verified {
		return r.finish(ctx, receipt, "succeeded", ActionResult{Allowed: true, State: "succeeded", Verified: true})
	}
	started := receipt.CreatedAt
	if receipt.DispatchedAt.Valid {
		started = receipt.DispatchedAt.Time
	}
	if !started.IsZero() && !started.Add(ambiguousReceiptVerificationLimit).After(now) {
		return r.finish(ctx, receipt, "failed", ActionResult{Allowed: true, State: "failed", Verified: false})
	}
	return r.finish(ctx, receipt, "ambiguous", ActionResult{Allowed: true, State: "ambiguous", Verified: false})
}

func (r *AmbiguousReceiptReconciler) decryptArguments(receipt sqlc.CharlieActionReceipt, descriptor CapabilityDescriptor, descriptorFound bool) (map[string]json.RawMessage, error) {
	if !descriptorFound || descriptor.Effect != EffectWrite || !descriptor.RequiresVerification || receipt.Effect != string(EffectWrite) || receipt.ArgumentsEncrypted == "" {
		return nil, fmt.Errorf("Charlie receipt capability cannot be reconciled")
	}
	plain, err := r.cipher.Decrypt(receipt.ArgumentsEncrypted)
	if err != nil || len(plain) == 0 || len(plain) > maxActionArguments {
		return nil, fmt.Errorf("Charlie receipt arguments are unavailable")
	}
	canonical, arguments, err := canonicalArguments(json.RawMessage(plain))
	if err != nil || digestBytes(canonical) != receipt.ArgumentDigest || !receiptArgumentsMatchDescriptor(descriptor, arguments) || validateCapabilityArguments(descriptor, arguments) != nil {
		return nil, fmt.Errorf("Charlie receipt argument integrity failed")
	}
	// The persisted arguments remain the exact signed model proposal so their
	// digest can be revalidated. Product adapters, including post-crash
	// verification, always receive Charlie's signed action ID as the trusted
	// operation identity.
	arguments = bindTrustedOperationID(arguments, receipt.CharlieActionID)
	return arguments, nil
}

func receiptArgumentsMatchDescriptor(descriptor CapabilityDescriptor, arguments map[string]json.RawMessage) bool {
	accepted := make(map[string]struct{}, len(descriptor.AcceptedFields))
	for _, name := range descriptor.AcceptedFields {
		accepted[name] = struct{}{}
	}
	for name := range arguments {
		if _, ok := accepted[name]; !ok {
			return false
		}
	}
	return true
}

func (r *AmbiguousReceiptReconciler) finish(ctx context.Context, receipt sqlc.CharlieActionReceipt, next string, result ActionResult) error {
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) == 0 || len(encoded) > maxActionResult+(16<<10) {
		return fmt.Errorf("encode Charlie reconciled result")
	}
	sealed, err := r.cipher.Encrypt(string(encoded))
	if err != nil || sealed == "" {
		return fmt.Errorf("encrypt Charlie reconciled result")
	}
	updated, err := r.queries.TransitionCharlieActionReceipt(ctx, sqlc.TransitionCharlieActionReceiptParams{
		NextState: next, ResultDigest: digestBytes(encoded), ResultStatus: result.State, ResultEncrypted: sealed,
		ID: receipt.ID, ExpectedState: receipt.State, LeaseOwner: r.leaseOwner, FencingEpoch: receipt.FencingEpoch,
	})
	if err != nil {
		return fmt.Errorf("commit Charlie receipt reconciliation: %w", err)
	}
	if err := r.auditor.RecordReceiptReconciliation(ctx, updated, next); err != nil {
		return fmt.Errorf("audit Charlie receipt reconciliation: %w", err)
	}
	return nil
}

type DBReceiptReconcileAuditor struct{ queries actionAuditQueries }

func NewDBReceiptReconcileAuditor(queries actionAuditQueries) (*DBReceiptReconcileAuditor, error) {
	if queries == nil {
		return nil, fmt.Errorf("Charlie receipt reconciliation auditor is unavailable")
	}
	return &DBReceiptReconcileAuditor{queries: queries}, nil
}

func (a *DBReceiptReconcileAuditor) RecordReceiptReconciliation(ctx context.Context, receipt sqlc.CharlieActionReceipt, outcome string) error {
	action := "charlie.action.reconciled_" + outcome
	detail, err := EncodeCharlieAuditDetail(action, "charlie_action", map[string]any{
		"outcome": outcome, "attempt": receipt.Attempt, "fencing_epoch": receipt.FencingEpoch,
		"action_digest": digestBytes([]byte(receipt.CharlieActionID)), "capability": receipt.Capability,
	})
	if err != nil {
		return err
	}
	status := int32(202)
	if outcome == "succeeded" {
		status = 200
	}
	return a.queries.CreateAuditLogV1(ctx, sqlc.CreateAuditLogV1Params{
		Source: "charlie_mcp", CorrelationID: receipt.AuditCorrelationID.String(), ActorAuthMethod: "charlie_reconciler",
		Action: action, ResourceType: "charlie_action",
		ResourceID: digestBytes([]byte(receipt.CharlieActionID)), HTTPMethod: "RECONCILE",
		StatusCode: status, Detail: detail, ActionClass: "system",
	})
}
