package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type fixedSafetyWindows struct{ windows []maintenance.Window }

func (f fixedSafetyWindows) Windows(context.Context) ([]maintenance.Window, error) {
	return f.windows, nil
}

type fakeProductSafetyQueries struct {
	connection sqlc.CharlieConnection
	session    sqlc.CharlieSession
	policy     sqlc.CharlieAutomationPolicy
	policyErr  error
	snapshot   sqlc.GetCharlieActionSafetySnapshotRow

	reserveMu    sync.Mutex
	reserved     bool
	reserveCalls atomic.Int32
	deferral     sqlc.CharlieActionDeferral
}

func (f *fakeProductSafetyQueries) GetCharlieConnectionByDeploymentID(context.Context, string) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}

func (f *fakeProductSafetyQueries) GetCharlieSessionByCentralID(context.Context, string) (sqlc.CharlieSession, error) {
	return f.session, nil
}

func (f *fakeProductSafetyQueries) GetCharlieAutomationPolicy(context.Context, sqlc.GetCharlieAutomationPolicyParams) (sqlc.CharlieAutomationPolicy, error) {
	return f.policy, f.policyErr
}

func (f *fakeProductSafetyQueries) GetCharlieActionSafetySnapshot(context.Context, sqlc.GetCharlieActionSafetySnapshotParams) (sqlc.GetCharlieActionSafetySnapshotRow, error) {
	return f.snapshot, nil
}

func (f *fakeProductSafetyQueries) ReserveCharlieAutoBudget(context.Context, sqlc.ReserveCharlieAutoBudgetParams) (sqlc.CharlieActionReceipt, error) {
	f.reserveCalls.Add(1)
	f.reserveMu.Lock()
	defer f.reserveMu.Unlock()
	if f.policyErr != nil || !f.policy.Enabled {
		return sqlc.CharlieActionReceipt{}, pgx.ErrNoRows
	}
	if f.reserved {
		return sqlc.CharlieActionReceipt{}, pgx.ErrNoRows
	}
	f.reserved = true
	return sqlc.CharlieActionReceipt{AutoBudgetReserved: true}, nil
}

func (f *fakeProductSafetyQueries) CreateCharlieActionDeferral(_ context.Context, arg sqlc.CreateCharlieActionDeferralParams) (sqlc.CharlieActionDeferral, error) {
	f.deferral = sqlc.CharlieActionDeferral{
		CharlieActionID: arg.CharlieActionID, WindowID: arg.WindowID,
		DeferredUntil: arg.DeferredUntil, ExpiresAt: arg.ExpiresAt,
	}
	return f.deferral, nil
}

func productSafetyFixture(t *testing.T) (*ProductActionSafety, *fakeProductSafetyQueries, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage) {
	t.Helper()
	connectionID, sessionID := uuid.New(), uuid.New()
	queries := &fakeProductSafetyQueries{
		connection: sqlc.CharlieConnection{ID: connectionID, DeploymentID: "deployment-a", Active: true},
		session:    sqlc.CharlieSession{ID: sessionID, ConnectionID: connectionID, CharlieSessionID: "session-a", State: "active"},
		policy: sqlc.CharlieAutomationPolicy{
			ConnectionID: connectionID, Capability: "astronomer.queue.retry_task", Enabled: true,
			MaxActionsPerIncident: 2, MaxActionsPerWindow: 1, BudgetWindowSeconds: 1800, CooldownSeconds: 300, Revision: 1,
		},
		snapshot: sqlc.GetCharlieActionSafetySnapshotRow{
			IncidentClear: true, CooldownClear: true, CircuitClosed: true,
			IncidentBudgetAvailable: true, WindowBudgetAvailable: true,
		},
	}
	safety, err := NewProductActionSafety(queries, fixedSafetyWindows{})
	if err != nil {
		t.Fatal(err)
	}
	action := ActionEnvelope{DeploymentID: "deployment-a", SessionID: "session-a", ActionID: "action-a"}
	capability, _ := capabilityByName("astronomer.queue.retry_task")
	arguments := map[string]json.RawMessage{"resource_id": json.RawMessage(`"resource-a"`), "task_id": json.RawMessage(`"task-a"`), "operation_id": json.RawMessage(`"operation-a"`)}
	return safety, queries, action, capability, arguments
}

func TestProductActionSafetyBudgetDigestIsKeyedByResourceID(t *testing.T) {
	first := map[string]json.RawMessage{
		"resource_id": json.RawMessage(`"resource-a"`), "task_id": json.RawMessage(`"task-a"`), "operation_id": json.RawMessage(`"action-a"`),
	}
	otherOperation := map[string]json.RawMessage{
		"resource_id": json.RawMessage(`"resource-a"`), "task_id": json.RawMessage(`"task-b"`), "operation_id": json.RawMessage(`"action-b"`),
	}
	otherResource := map[string]json.RawMessage{
		"resource_id": json.RawMessage(`"resource-b"`), "task_id": json.RawMessage(`"task-a"`), "operation_id": json.RawMessage(`"action-a"`),
	}
	if resourceDigest("astronomer.queue.retry_task", first) != resourceDigest("astronomer.queue.retry_task", otherOperation) {
		t.Fatal("auto safety budget changed when only adapter operation semantics changed")
	}
	if resourceDigest("astronomer.queue.retry_task", first) == resourceDigest("astronomer.queue.retry_task", otherResource) {
		t.Fatal("distinct ProductContext resources shared an auto safety budget key")
	}
}

func TestProductActionSafetyAutoIsFailClosedWithoutExplicitPolicy(t *testing.T) {
	safety, queries, action, capability, arguments := productSafetyFixture(t)
	queries.policyErr = pgx.ErrNoRows
	facts, err := safety.Evaluate(context.Background(), action, capability, arguments)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Allowlisted || facts.BudgetAvailable {
		t.Fatalf("auto unexpectedly enabled without policy: %+v", facts)
	}
	if err := safety.ConsumeAutoBudget(context.Background(), action, capability, arguments); err == nil {
		t.Fatal("auto budget reserved without an explicit policy")
	}
}

func TestProductActionSafetyMaintenanceRefuseAndDefer(t *testing.T) {
	safety, queries, action, capability, arguments := productSafetyFixture(t)
	now := time.Date(2026, 8, 5, 9, 0, 30, 0, time.UTC)
	safety.now = func() time.Time { return now }
	window := maintenance.Window{
		ID: uuid.New(), Name: "change-freeze", Mode: maintenance.ModeBlackout,
		CronOpen: "* * * * *", DurationMinutes: 1, Timezone: "UTC",
		OperationTypes: []string{maintenance.OpCharlieAction}, OnBlock: maintenance.OnBlockRefuse, Enabled: true,
	}
	safety.windows = fixedSafetyWindows{windows: []maintenance.Window{window}}
	facts, err := safety.Evaluate(context.Background(), action, capability, arguments)
	if err != nil || facts.MaintenanceClear || !facts.PreconditionsMet {
		t.Fatalf("maintenance refusal not enforced: facts=%+v err=%v", facts, err)
	}

	window.OnBlock = maintenance.OnBlockDefer
	safety.windows = fixedSafetyWindows{windows: []maintenance.Window{window}}
	err = safety.CommitWrite(context.Background(), action, capability, arguments, ModeApproval)
	var deferred *ActionDeferredError
	if !errors.As(err, &deferred) {
		t.Fatalf("maintenance defer=%v, want ActionDeferredError", err)
	}
	if queries.deferral.CharlieActionID != action.ActionID || deferred.DeferredUntil.IsZero() || !deferred.ExpiresAt.After(deferred.DeferredUntil) {
		t.Fatalf("deferral was not durable and bounded: %+v", queries.deferral)
	}
}

func TestProductActionSafetyConcurrentAutoBudgetAllowsOneClaim(t *testing.T) {
	safety, queries, action, capability, arguments := productSafetyFixture(t)
	const callers = 16
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if safety.ConsumeAutoBudget(context.Background(), action, capability, arguments) == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 || queries.reserveCalls.Load() != callers {
		t.Fatalf("successes=%d reserve_calls=%d, want one durable winner", successes.Load(), queries.reserveCalls.Load())
	}
}
