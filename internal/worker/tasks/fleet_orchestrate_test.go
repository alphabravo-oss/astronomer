package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ─────────────────────────────────────────────────────────────────────
// Fakes
// ─────────────────────────────────────────────────────────────────────

// fakeFleetQuerier is the narrow FleetOrchestrateQuerier surface that
// every orchestrator test exercises. Mutations are recorded so each
// test can assert on the end state without re-reading.
type fakeFleetQuerier struct {
	mu sync.Mutex

	ops     map[uuid.UUID]sqlc.FleetOperation
	targets map[uuid.UUID]map[uuid.UUID]sqlc.FleetOperationTarget

	clusters []sqlc.ListClustersForSelectorEvaluationRow

	// Sub-operation state. tool_ops keyed by id; template_apps keyed
	// by cluster_id.
	toolOps      map[uuid.UUID]sqlc.ToolOperation
	templateApps map[uuid.UUID]sqlc.ClusterTemplateApplication
	templates    map[uuid.UUID]sqlc.ClusterTemplate
}

func newFakeFleetQuerier() *fakeFleetQuerier {
	return &fakeFleetQuerier{
		ops:          map[uuid.UUID]sqlc.FleetOperation{},
		targets:      map[uuid.UUID]map[uuid.UUID]sqlc.FleetOperationTarget{},
		toolOps:      map[uuid.UUID]sqlc.ToolOperation{},
		templateApps: map[uuid.UUID]sqlc.ClusterTemplateApplication{},
		templates:    map[uuid.UUID]sqlc.ClusterTemplate{},
	}
}

func (f *fakeFleetQuerier) ListPendingFleetOperations(_ context.Context, _ int32) ([]sqlc.FleetOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.FleetOperation{}
	for _, op := range f.ops {
		if op.Status == FleetOpStatusPending || op.Status == FleetOpStatusRunning {
			out = append(out, op)
		}
	}
	return out, nil
}

func (f *fakeFleetQuerier) GetFleetOperation(_ context.Context, id uuid.UUID) (sqlc.FleetOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op, ok := f.ops[id]
	if !ok {
		return sqlc.FleetOperation{}, pgx.ErrNoRows
	}
	return op, nil
}

func (f *fakeFleetQuerier) MarkFleetOperationTransition(_ context.Context, arg sqlc.MarkFleetOperationTransitionParams) (sqlc.FleetOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op, ok := f.ops[arg.ID]
	if !ok {
		return sqlc.FleetOperation{}, pgx.ErrNoRows
	}
	if op.Status != arg.FromStatus {
		return sqlc.FleetOperation{}, pgx.ErrNoRows
	}
	op.Status = arg.ToStatus
	if !op.StartedAt.Valid && arg.StartedAt.Valid {
		op.StartedAt = arg.StartedAt
	}
	if arg.CompletedAt.Valid {
		op.CompletedAt = arg.CompletedAt
	}
	op.LastError = arg.LastError
	op.UpdatedAt = time.Now()
	f.ops[arg.ID] = op
	return op, nil
}

func (f *fakeFleetQuerier) SetFleetOperationStatus(_ context.Context, arg sqlc.SetFleetOperationStatusParams) (sqlc.FleetOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op, ok := f.ops[arg.ID]
	if !ok {
		return sqlc.FleetOperation{}, pgx.ErrNoRows
	}
	op.Status = arg.Status
	op.LastError = arg.LastError
	f.ops[arg.ID] = op
	return op, nil
}

func (f *fakeFleetQuerier) UpdateFleetOperationCounters(_ context.Context, arg sqlc.UpdateFleetOperationCountersParams) (sqlc.FleetOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op, ok := f.ops[arg.ID]
	if !ok {
		return sqlc.FleetOperation{}, pgx.ErrNoRows
	}
	op.TotalClusters = arg.TotalClusters
	op.CompletedClusters = arg.CompletedClusters
	op.FailedClusters = arg.FailedClusters
	op.SkippedClusters = arg.SkippedClusters
	f.ops[arg.ID] = op
	return op, nil
}

func (f *fakeFleetQuerier) CreateFleetOperationTarget(_ context.Context, arg sqlc.CreateFleetOperationTargetParams) (sqlc.FleetOperationTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.targets[arg.OperationID] == nil {
		f.targets[arg.OperationID] = map[uuid.UUID]sqlc.FleetOperationTarget{}
	}
	for _, t := range f.targets[arg.OperationID] {
		if t.ClusterID == arg.ClusterID {
			return sqlc.FleetOperationTarget{}, pgx.ErrNoRows // ON CONFLICT DO NOTHING
		}
	}
	t := sqlc.FleetOperationTarget{
		ID:               uuid.New(),
		OperationID:      arg.OperationID,
		ClusterID:        arg.ClusterID,
		Status:           FleetTargetStatusPending,
		SubOperationType: arg.SubOperationType,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	f.targets[arg.OperationID][t.ID] = t
	return t, nil
}

func (f *fakeFleetQuerier) ListPendingTargetsForOperation(_ context.Context, arg sqlc.ListPendingTargetsForOperationParams) ([]sqlc.FleetOperationTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.FleetOperationTarget{}
	for _, t := range f.targets[arg.OperationID] {
		if t.Status == FleetTargetStatusPending {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeFleetQuerier) ListRunningTargetsForOperation(_ context.Context, opID uuid.UUID) ([]sqlc.FleetOperationTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.FleetOperationTarget{}
	for _, t := range f.targets[opID] {
		if t.Status == FleetTargetStatusRunning {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeFleetQuerier) CountRunningTargetsForOperation(_ context.Context, opID uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, t := range f.targets[opID] {
		if t.Status == FleetTargetStatusRunning {
			n++
		}
	}
	return n, nil
}

func (f *fakeFleetQuerier) CountFleetOperationTargetsByStatus(_ context.Context, opID uuid.UUID) ([]sqlc.CountFleetOperationTargetsByStatusRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	counts := map[string]int64{}
	for _, t := range f.targets[opID] {
		counts[t.Status]++
	}
	out := make([]sqlc.CountFleetOperationTargetsByStatusRow, 0, len(counts))
	for status, n := range counts {
		out = append(out, sqlc.CountFleetOperationTargetsByStatusRow{Status: status, N: n})
	}
	return out, nil
}

func (f *fakeFleetQuerier) CountTerminalTargetsForOperation(_ context.Context, opID uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, t := range f.targets[opID] {
		switch t.Status {
		case FleetTargetStatusCompleted, FleetTargetStatusFailed,
			FleetTargetStatusSkipped, FleetTargetStatusAborted:
			n++
		}
	}
	return n, nil
}

func (f *fakeFleetQuerier) MarkFleetTargetDispatched(_ context.Context, arg sqlc.MarkFleetTargetDispatchedParams) (sqlc.FleetOperationTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for opID, m := range f.targets {
		for id, t := range m {
			if id == arg.ID {
				if t.Status != FleetTargetStatusPending {
					return sqlc.FleetOperationTarget{}, pgx.ErrNoRows
				}
				t.Status = FleetTargetStatusRunning
				t.SubOperationID = arg.SubOperationID
				t.SubOperationType = arg.SubOperationType
				t.StartedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
				f.targets[opID][id] = t
				return t, nil
			}
		}
	}
	return sqlc.FleetOperationTarget{}, pgx.ErrNoRows
}

func (f *fakeFleetQuerier) MarkFleetTargetCompleted(_ context.Context, id uuid.UUID) (sqlc.FleetOperationTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for opID, m := range f.targets {
		if t, ok := m[id]; ok {
			t.Status = FleetTargetStatusCompleted
			t.CompletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			t.LastError = ""
			f.targets[opID][id] = t
			return t, nil
		}
	}
	return sqlc.FleetOperationTarget{}, pgx.ErrNoRows
}

func (f *fakeFleetQuerier) MarkFleetTargetFailed(_ context.Context, arg sqlc.MarkFleetTargetFailedParams) (sqlc.FleetOperationTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for opID, m := range f.targets {
		if t, ok := m[arg.ID]; ok {
			t.Status = FleetTargetStatusFailed
			t.CompletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			t.LastError = arg.LastError
			f.targets[opID][arg.ID] = t
			return t, nil
		}
	}
	return sqlc.FleetOperationTarget{}, pgx.ErrNoRows
}

func (f *fakeFleetQuerier) MarkFleetTargetSkipped(_ context.Context, arg sqlc.MarkFleetTargetSkippedParams) (sqlc.FleetOperationTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for opID, m := range f.targets {
		if t, ok := m[arg.ID]; ok {
			t.Status = FleetTargetStatusSkipped
			t.CompletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			t.LastError = arg.LastError
			f.targets[opID][arg.ID] = t
			return t, nil
		}
	}
	return sqlc.FleetOperationTarget{}, pgx.ErrNoRows
}

func (f *fakeFleetQuerier) ListClustersForSelectorEvaluation(_ context.Context) ([]sqlc.ListClustersForSelectorEvaluationRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sqlc.ListClustersForSelectorEvaluationRow{}, f.clusters...), nil
}

func (f *fakeFleetQuerier) GetToolOperation(_ context.Context, id uuid.UUID) (sqlc.ToolOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op, ok := f.toolOps[id]
	if !ok {
		return sqlc.ToolOperation{}, pgx.ErrNoRows
	}
	return op, nil
}

func (f *fakeFleetQuerier) GetClusterTemplateApplication(_ context.Context, clusterID uuid.UUID) (sqlc.ClusterTemplateApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	app, ok := f.templateApps[clusterID]
	if !ok {
		return sqlc.ClusterTemplateApplication{}, pgx.ErrNoRows
	}
	return app, nil
}

func (f *fakeFleetQuerier) GetClusterTemplateByID(_ context.Context, id uuid.UUID) (sqlc.ClusterTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.templates[id]
	if !ok {
		return sqlc.ClusterTemplate{}, pgx.ErrNoRows
	}
	return t, nil
}

func (f *fakeFleetQuerier) UpsertClusterTemplateApplication(_ context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	app := sqlc.ClusterTemplateApplication{
		ClusterID:    arg.ClusterID,
		TemplateID:   arg.TemplateID,
		Status:       "pending",
		SpecSnapshot: arg.SpecSnapshot,
	}
	f.templateApps[arg.ClusterID] = app
	return app, nil
}

// ─────────────────────────────────────────────────────────────────────
// fakeFleetDispatcher
// ─────────────────────────────────────────────────────────────────────

// fakeFleetDispatcher records every dispatch + lets tests choose to
// fail the call. The synthesised sub-operation IDs are wired back
// into the fake querier so the orchestrator's poll path finds the
// matching ToolOperation / ClusterTemplateApplication row.
type fakeFleetDispatcher struct {
	mu sync.Mutex

	q             *fakeFleetQuerier
	failClusters  map[uuid.UUID]bool
	dispatched    int
	dispatchedFor []uuid.UUID

	// resultStatus is the status the synthesised sub-op rows start at
	// — defaults to "pending". Tests that want to fast-path to
	// completed/failed set this.
	resultStatus string
}

func newFakeFleetDispatcher(q *fakeFleetQuerier) *fakeFleetDispatcher {
	return &fakeFleetDispatcher{q: q, failClusters: map[uuid.UUID]bool{}, resultStatus: "pending"}
}

func (d *fakeFleetDispatcher) DispatchToolOperation(_ context.Context, kind string, clusterID uuid.UUID, _ FleetToolOperationSpec) (uuid.UUID, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dispatched++
	d.dispatchedFor = append(d.dispatchedFor, clusterID)
	if d.failClusters[clusterID] {
		return uuid.Nil, "", errors.New("forced dispatch failure")
	}
	subID := uuid.New()
	d.q.mu.Lock()
	d.q.toolOps[subID] = sqlc.ToolOperation{
		ID:            subID,
		TargetType:    "tool_installation",
		OperationType: kind,
		Status:        d.resultStatus,
	}
	d.q.mu.Unlock()
	return subID, kind, nil
}

func (d *fakeFleetDispatcher) DispatchApplyTemplate(_ context.Context, clusterID, _ uuid.UUID) (uuid.UUID, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dispatched++
	d.dispatchedFor = append(d.dispatchedFor, clusterID)
	subID := clusterID
	return subID, FleetOpTypeApplyTemplate, nil
}

// completeSubOp manually moves a synthesised sub-op to "completed".
// Used by tests that want to simulate a successful per-cluster
// operation.
func (d *fakeFleetDispatcher) completeSubOp(subID uuid.UUID) {
	d.q.mu.Lock()
	defer d.q.mu.Unlock()
	op := d.q.toolOps[subID]
	op.Status = "completed"
	d.q.toolOps[subID] = op
}

// failSubOp moves a synthesised sub-op to "failed" with an error.
func (d *fakeFleetDispatcher) failSubOp(subID uuid.UUID, msg string) {
	d.q.mu.Lock()
	defer d.q.mu.Unlock()
	op := d.q.toolOps[subID]
	op.Status = "failed"
	op.ErrorMessage = msg
	d.q.toolOps[subID] = op
}

// completeAllRunning bulk-transitions every running target's sub-op
// to "completed". Useful for tests that drive the orchestrator
// through multiple ticks.
func (d *fakeFleetDispatcher) completeAllRunning(opID uuid.UUID) {
	d.q.mu.Lock()
	defer d.q.mu.Unlock()
	for _, t := range d.q.targets[opID] {
		if t.Status == FleetTargetStatusRunning && t.SubOperationID.Valid {
			subID := uuid.UUID(t.SubOperationID.Bytes)
			op := d.q.toolOps[subID]
			op.Status = "completed"
			d.q.toolOps[subID] = op
		}
	}
}

// ─────────────────────────────────────────────────────────────────────
// mwFalse / mwAlways
// ─────────────────────────────────────────────────────────────────────

type mwAlways struct {
	called []uuid.UUID
}

func (m *mwAlways) IsInMaintenanceWindow(_ context.Context, c uuid.UUID) bool {
	m.called = append(m.called, c)
	return true
}

// ─────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────

func mkFleetOp(name, opType, strategy, onError string, maxConcurrent int32, selector map[string]any) sqlc.FleetOperation {
	selBytes, _ := json.Marshal(selector)
	specBytes, _ := json.Marshal(map[string]any{"slug": "cert-manager", "target_version": "v1.14.5"})
	return sqlc.FleetOperation{
		ID:            uuid.New(),
		Name:          name,
		OperationType: opType,
		Selector:      selBytes,
		OperationSpec: specBytes,
		Strategy:      strategy,
		MaxConcurrent: maxConcurrent,
		OnError:       onError,
		Status:        FleetOpStatusPending,
	}
}

// seedClusters wires N clusters into the fake querier with the given
// labels (same labels for every cluster — the selector tests use
// per-cluster label variation by calling addCluster directly).
func seedClusters(q *fakeFleetQuerier, n int, labels map[string]string) []uuid.UUID {
	ids := make([]uuid.UUID, 0, n)
	labelsRaw, _ := json.Marshal(labels)
	for i := 0; i < n; i++ {
		c := sqlc.ListClustersForSelectorEvaluationRow{
			ID:     uuid.New(),
			Name:   "cluster-" + uuid.New().String()[:8],
			Labels: labelsRaw,
		}
		q.clusters = append(q.clusters, c)
		ids = append(ids, c.ID)
	}
	return ids
}

// addCluster registers one cluster with custom labels.
func addCluster(q *fakeFleetQuerier, labels map[string]string) uuid.UUID {
	labelsRaw, _ := json.Marshal(labels)
	c := sqlc.ListClustersForSelectorEvaluationRow{
		ID:     uuid.New(),
		Name:   "cluster-" + uuid.New().String()[:8],
		Labels: labelsRaw,
	}
	q.clusters = append(q.clusters, c)
	return c.ID
}

// ─────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────

// TestFleetOperation_LaunchEvaluatesSelector seeds a mix of clusters
// and asserts the orchestrator inserts target rows only for those
// that match the selector. The operation transitions pending → running
// after the targets land.
func TestFleetOperation_LaunchEvaluatesSelector(t *testing.T) {
	q := newFakeFleetQuerier()

	// Three clusters: two staging, one prod.
	matched1 := addCluster(q, map[string]string{"tier": "staging", "region": "us-east"})
	matched2 := addCluster(q, map[string]string{"tier": "staging", "region": "us-west"})
	_ = addCluster(q, map[string]string{"tier": "prod", "region": "us-east"})

	op := mkFleetOp("staging-upgrade", FleetOpTypeToolUpgrade, FleetStrategyParallel, FleetOnErrorAbort, 3,
		map[string]any{"matchLabels": map[string]string{"tier": "staging"}})
	q.ops[op.ID] = op

	d := newFakeFleetDispatcher(q)
	deps := FleetOrchestrateDeps{Queries: q, Dispatcher: d, MaintenanceWindow: NoopMaintenanceWindowChecker{}}

	if err := runFleetOrchestrateTick(context.Background(), deps); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := len(q.targets[op.ID]); got != 2 {
		t.Errorf("expected 2 targets, got %d", got)
	}
	clusterSet := map[uuid.UUID]bool{}
	for _, t := range q.targets[op.ID] {
		clusterSet[t.ClusterID] = true
	}
	if !clusterSet[matched1] || !clusterSet[matched2] {
		t.Errorf("matched cluster IDs missing from targets")
	}
	if q.ops[op.ID].Status != FleetOpStatusRunning {
		t.Errorf("expected running, got %q", q.ops[op.ID].Status)
	}
}

// TestFleetOrchestrator_RespectsMaxConcurrent ensures the orchestrator
// never dispatches more than max_concurrent targets in flight.
func TestFleetOrchestrator_RespectsMaxConcurrent(t *testing.T) {
	q := newFakeFleetQuerier()
	seedClusters(q, 10, map[string]string{"tier": "staging"})

	op := mkFleetOp("staging-roll", FleetOpTypeToolUpgrade, FleetStrategyParallel, FleetOnErrorContinue, 3,
		map[string]any{"matchLabels": map[string]string{"tier": "staging"}})
	q.ops[op.ID] = op
	d := newFakeFleetDispatcher(q)
	deps := FleetOrchestrateDeps{Queries: q, Dispatcher: d, MaintenanceWindow: NoopMaintenanceWindowChecker{}}

	// Tick 1: launch (creates targets, transitions to running, then
	// dispatches up to max_concurrent on the same tick because the
	// orchestrator re-reads op after the launch).
	if err := runFleetOrchestrateTick(context.Background(), deps); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	// Run a second tick — the running tick should NOT dispatch more
	// than max_concurrent.
	if err := runFleetOrchestrateTick(context.Background(), deps); err != nil {
		t.Fatalf("tick 2: %v", err)
	}

	running := 0
	for _, t := range q.targets[op.ID] {
		if t.Status == FleetTargetStatusRunning {
			running++
		}
	}
	if running > 3 {
		t.Errorf("expected <= 3 running, got %d", running)
	}
}

// TestFleetOrchestrator_SequentialMode runs strategy=sequential and
// asserts only one cluster runs at a time even though max_concurrent
// might be honoured laxly.
func TestFleetOrchestrator_SequentialMode(t *testing.T) {
	q := newFakeFleetQuerier()
	seedClusters(q, 5, map[string]string{"tier": "staging"})

	op := mkFleetOp("seq", FleetOpTypeToolUpgrade, FleetStrategySequential, FleetOnErrorContinue, 1,
		map[string]any{"matchLabels": map[string]string{"tier": "staging"}})
	q.ops[op.ID] = op
	d := newFakeFleetDispatcher(q)
	deps := FleetOrchestrateDeps{Queries: q, Dispatcher: d, MaintenanceWindow: NoopMaintenanceWindowChecker{}}

	if err := runFleetOrchestrateTick(context.Background(), deps); err != nil {
		t.Fatalf("tick: %v", err)
	}
	running := 0
	for _, t := range q.targets[op.ID] {
		if t.Status == FleetTargetStatusRunning {
			running++
		}
	}
	if running != 1 {
		t.Errorf("sequential mode dispatched %d concurrently, want 1", running)
	}
}

// TestFleetOrchestrator_AbortsOnFirstFailure verifies on_error=abort
// flips the operation to "aborted" on the first failed target.
func TestFleetOrchestrator_AbortsOnFirstFailure(t *testing.T) {
	q := newFakeFleetQuerier()
	clusterIDs := seedClusters(q, 5, map[string]string{"tier": "staging"})

	op := mkFleetOp("abort-on-fail", FleetOpTypeToolUpgrade, FleetStrategyParallel, FleetOnErrorAbort, 3,
		map[string]any{"matchLabels": map[string]string{"tier": "staging"}})
	q.ops[op.ID] = op
	d := newFakeFleetDispatcher(q)
	deps := FleetOrchestrateDeps{Queries: q, Dispatcher: d, MaintenanceWindow: NoopMaintenanceWindowChecker{}}

	// Tick 1: launch + dispatch.
	if err := runFleetOrchestrateTick(context.Background(), deps); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	// Fail every dispatched sub-op.
	for _, t := range q.targets[op.ID] {
		if t.Status == FleetTargetStatusRunning {
			d.failSubOp(uuid.UUID(t.SubOperationID.Bytes), "boom")
		}
	}
	// Tick 2: poll, see the failure, abort.
	if err := runFleetOrchestrateTick(context.Background(), deps); err != nil {
		t.Fatalf("tick 2: %v", err)
	}

	if q.ops[op.ID].Status != FleetOpStatusAborted {
		t.Errorf("expected aborted, got %q", q.ops[op.ID].Status)
	}
	// No further targets should be dispatched on subsequent ticks.
	if err := runFleetOrchestrateTick(context.Background(), deps); err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	// 5 clusters total — only the first batch (up to max_concurrent=3)
	// got dispatched.
	if d.dispatched > 3 {
		t.Errorf("expected at most 3 dispatches, got %d", d.dispatched)
	}
	_ = clusterIDs
}

// TestFleetOrchestrator_ContinuesOnFailureMode verifies on_error=continue
// keeps dispatching after a failure.
func TestFleetOrchestrator_ContinuesOnFailureMode(t *testing.T) {
	q := newFakeFleetQuerier()
	seedClusters(q, 5, map[string]string{"tier": "staging"})

	op := mkFleetOp("keep-going", FleetOpTypeToolUpgrade, FleetStrategyParallel, FleetOnErrorContinue, 3,
		map[string]any{"matchLabels": map[string]string{"tier": "staging"}})
	q.ops[op.ID] = op
	d := newFakeFleetDispatcher(q)
	deps := FleetOrchestrateDeps{Queries: q, Dispatcher: d, MaintenanceWindow: NoopMaintenanceWindowChecker{}}

	if err := runFleetOrchestrateTick(context.Background(), deps); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	// Fail every running sub-op.
	failedSubs := 0
	for _, t := range q.targets[op.ID] {
		if t.Status == FleetTargetStatusRunning {
			d.failSubOp(uuid.UUID(t.SubOperationID.Bytes), "boom")
			failedSubs++
		}
	}
	// Run additional ticks until everything terminates.
	for i := 0; i < 5; i++ {
		if err := runFleetOrchestrateTick(context.Background(), deps); err != nil {
			t.Fatalf("tick: %v", err)
		}
		// Fail every newly-running sub-op too.
		for _, t := range q.targets[op.ID] {
			if t.Status == FleetTargetStatusRunning {
				d.failSubOp(uuid.UUID(t.SubOperationID.Bytes), "boom")
			}
		}
	}
	if q.ops[op.ID].Status == FleetOpStatusAborted {
		t.Errorf("on_error=continue should NOT abort, status=%q", q.ops[op.ID].Status)
	}
	if d.dispatched < 5 {
		t.Errorf("expected continue mode to dispatch all 5 clusters, got %d", d.dispatched)
	}
}

// TestFleetOrchestrator_SkipsClustersInMaintenanceWindow verifies the
// orchestrator consults the MaintenanceWindowChecker and skips a
// target when it reports a window is open. The target stays pending
// so a future tick can re-try after the window closes.
func TestFleetOrchestrator_SkipsClustersInMaintenanceWindow(t *testing.T) {
	q := newFakeFleetQuerier()
	seedClusters(q, 3, map[string]string{"tier": "staging"})

	op := mkFleetOp("mw", FleetOpTypeToolUpgrade, FleetStrategyParallel, FleetOnErrorContinue, 3,
		map[string]any{"matchLabels": map[string]string{"tier": "staging"}})
	op.RespectMaintenanceWindows = true
	q.ops[op.ID] = op

	d := newFakeFleetDispatcher(q)
	mw := &mwAlways{}
	deps := FleetOrchestrateDeps{Queries: q, Dispatcher: d, MaintenanceWindow: mw}

	if err := runFleetOrchestrateTick(context.Background(), deps); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// The orchestrator should have created targets (selector matched
	// all 3) but dispatched zero because all are in maintenance.
	if got := len(q.targets[op.ID]); got != 3 {
		t.Errorf("expected 3 targets created, got %d", got)
	}
	if d.dispatched != 0 {
		t.Errorf("expected 0 dispatches (all in maintenance window), got %d", d.dispatched)
	}
	if len(mw.called) == 0 {
		t.Errorf("expected MaintenanceWindowChecker to be consulted")
	}
}

// TestFleetOrchestrator_CompletesWhenAllTargetsSucceed walks a happy
// path: launch, dispatch, sub-ops complete, operation terminates as
// "completed".
func TestFleetOrchestrator_CompletesWhenAllTargetsSucceed(t *testing.T) {
	q := newFakeFleetQuerier()
	seedClusters(q, 2, map[string]string{"tier": "staging"})

	op := mkFleetOp("happy", FleetOpTypeToolUpgrade, FleetStrategyParallel, FleetOnErrorAbort, 5,
		map[string]any{"matchLabels": map[string]string{"tier": "staging"}})
	q.ops[op.ID] = op
	d := newFakeFleetDispatcher(q)
	deps := FleetOrchestrateDeps{Queries: q, Dispatcher: d, MaintenanceWindow: NoopMaintenanceWindowChecker{}}

	if err := runFleetOrchestrateTick(context.Background(), deps); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	d.completeAllRunning(op.ID)
	if err := runFleetOrchestrateTick(context.Background(), deps); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if q.ops[op.ID].Status != FleetOpStatusCompleted {
		t.Errorf("expected completed, got %q", q.ops[op.ID].Status)
	}
}

// TestFleetOrchestrator_IdempotentOnDoubleTick verifies a second tick
// over an already-launched operation doesn't re-create target rows.
// Load-bearing: the migration's ON CONFLICT DO NOTHING is the
// belt-and-suspenders backstop; the orchestrator must also not try.
func TestFleetOrchestrator_IdempotentOnDoubleTick(t *testing.T) {
	q := newFakeFleetQuerier()
	seedClusters(q, 3, map[string]string{"tier": "staging"})

	op := mkFleetOp("once", FleetOpTypeToolUpgrade, FleetStrategyParallel, FleetOnErrorContinue, 3,
		map[string]any{"matchLabels": map[string]string{"tier": "staging"}})
	q.ops[op.ID] = op
	d := newFakeFleetDispatcher(q)
	deps := FleetOrchestrateDeps{Queries: q, Dispatcher: d, MaintenanceWindow: NoopMaintenanceWindowChecker{}}

	if err := runFleetOrchestrateTick(context.Background(), deps); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	originalCount := len(q.targets[op.ID])
	// Second tick should not create new target rows even though the
	// selector still matches the same clusters.
	if err := runFleetOrchestrateTick(context.Background(), deps); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if got := len(q.targets[op.ID]); got != originalCount {
		t.Errorf("targets count changed: %d -> %d", originalCount, got)
	}
}

// TestFleetSelector_MatchLabels exercises the selector evaluator
// directly. matchLabels is the workhorse so it gets thorough coverage.
func TestFleetSelector_MatchLabels(t *testing.T) {
	cands := []FleetClusterCandidate{
		{ID: uuid.New(), Name: "a", Labels: map[string]string{"tier": "staging", "region": "us-east"}},
		{ID: uuid.New(), Name: "b", Labels: map[string]string{"tier": "staging", "region": "us-west"}},
		{ID: uuid.New(), Name: "c", Labels: map[string]string{"tier": "prod", "region": "us-east"}},
		{ID: uuid.New(), Name: "d", Labels: map[string]string{}},
	}

	sel := FleetSelector{MatchLabels: map[string]string{"tier": "staging"}}
	got := EvaluateFleetSelector(sel, cands)
	if len(got) != 2 {
		t.Errorf("matchLabels tier=staging: got %d, want 2", len(got))
	}

	sel = FleetSelector{MatchLabels: map[string]string{"tier": "staging", "region": "us-east"}}
	got = EvaluateFleetSelector(sel, cands)
	if len(got) != 1 || got[0].Name != "a" {
		t.Errorf("AND of tier+region: got %d, want 1 (a)", len(got))
	}

	// Empty selector matches nothing.
	got = EvaluateFleetSelector(FleetSelector{}, cands)
	if len(got) != 0 {
		t.Errorf("empty selector matched %d clusters; want 0 (load-bearing safety)", len(got))
	}
}

// TestFleetSelector_MatchExpressions exercises the In/NotIn/Exists
// operators.
func TestFleetSelector_MatchExpressions(t *testing.T) {
	cands := []FleetClusterCandidate{
		{ID: uuid.New(), Name: "a", Labels: map[string]string{"region": "us-east"}},
		{ID: uuid.New(), Name: "b", Labels: map[string]string{"region": "us-west"}},
		{ID: uuid.New(), Name: "c", Labels: map[string]string{"region": "eu-west"}},
		{ID: uuid.New(), Name: "d", Labels: map[string]string{}},
	}

	// In
	sel := FleetSelector{MatchExpressions: []FleetSelectorExpression{
		{Key: "region", Operator: "In", Values: []string{"us-east", "us-west"}},
	}}
	got := EvaluateFleetSelector(sel, cands)
	if len(got) != 2 {
		t.Errorf("In: got %d, want 2", len(got))
	}

	// NotIn — matches absence too.
	sel = FleetSelector{MatchExpressions: []FleetSelectorExpression{
		{Key: "region", Operator: "NotIn", Values: []string{"us-east"}},
	}}
	got = EvaluateFleetSelector(sel, cands)
	if len(got) != 3 {
		t.Errorf("NotIn: got %d, want 3 (b, c, d)", len(got))
	}

	// Exists / DoesNotExist
	sel = FleetSelector{MatchExpressions: []FleetSelectorExpression{
		{Key: "region", Operator: "Exists"},
	}}
	got = EvaluateFleetSelector(sel, cands)
	if len(got) != 3 {
		t.Errorf("Exists: got %d, want 3", len(got))
	}

	sel = FleetSelector{MatchExpressions: []FleetSelectorExpression{
		{Key: "region", Operator: "DoesNotExist"},
	}}
	got = EvaluateFleetSelector(sel, cands)
	if len(got) != 1 || got[0].Name != "d" {
		t.Errorf("DoesNotExist: got %d, want 1 (d)", len(got))
	}
}
