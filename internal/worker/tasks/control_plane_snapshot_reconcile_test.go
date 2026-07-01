package tasks

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeCPSQuerier implements ControlPlaneSnapshotSweepQuerier; only the
// reconcile surface is exercised here, the rest return zero values.
type fakeCPSQuerier struct {
	running   []sqlc.ControlPlaneSnapshot
	succeeded []uuid.UUID
	failed    map[uuid.UUID]string
}

func (f *fakeCPSQuerier) GetPlatformSetting(context.Context, string) (sqlc.PlatformSetting, error) {
	return sqlc.PlatformSetting{}, nil
}
func (f *fakeCPSQuerier) ListClusters(context.Context, sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	return nil, nil
}
func (f *fakeCPSQuerier) GetLatestControlPlaneSnapshotByCluster(context.Context, uuid.UUID) (sqlc.ControlPlaneSnapshot, error) {
	return sqlc.ControlPlaneSnapshot{}, nil
}
func (f *fakeCPSQuerier) CreateControlPlaneSnapshot(context.Context, sqlc.CreateControlPlaneSnapshotParams) (sqlc.ControlPlaneSnapshot, error) {
	return sqlc.ControlPlaneSnapshot{}, nil
}
func (f *fakeCPSQuerier) MarkControlPlaneSnapshotStatus(context.Context, sqlc.MarkControlPlaneSnapshotStatusParams) error {
	return nil
}
func (f *fakeCPSQuerier) MarkControlPlaneSnapshotSucceeded(_ context.Context, arg sqlc.MarkControlPlaneSnapshotSucceededParams) error {
	f.succeeded = append(f.succeeded, arg.ID)
	return nil
}
func (f *fakeCPSQuerier) MarkControlPlaneSnapshotFailed(_ context.Context, arg sqlc.MarkControlPlaneSnapshotFailedParams) error {
	if f.failed == nil {
		f.failed = map[uuid.UUID]string{}
	}
	f.failed[arg.ID] = arg.Error
	return nil
}
func (f *fakeCPSQuerier) ListRunningControlPlaneSnapshots(_ context.Context, arg sqlc.ListRunningControlPlaneSnapshotsParams) ([]sqlc.ControlPlaneSnapshot, error) {
	if arg.Offset > 0 {
		return nil, nil // single page in tests
	}
	return f.running, nil
}
func (f *fakeCPSQuerier) PruneControlPlaneSnapshots(context.Context, sqlc.PruneControlPlaneSnapshotsParams) error {
	return nil
}

func TestReconcileControlPlaneSnapshots_PhaseMapping(t *testing.T) {
	succeededID, failedID, goneID, runningID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	phases := map[uuid.UUID]string{
		succeededID: "succeeded",
		failedID:    "failed",
		goneID:      "gone",
		runningID:   "running",
	}
	q := &fakeCPSQuerier{running: []sqlc.ControlPlaneSnapshot{
		{ID: succeededID, ClusterID: uuid.New()},
		{ID: failedID, ClusterID: uuid.New()},
		{ID: goneID, ClusterID: uuid.New()},
		{ID: runningID, ClusterID: uuid.New()},
	}}

	// Stub the handler-side reader.
	prev := controlPlaneSnapshotStatusReader
	defer func() { controlPlaneSnapshotStatusReader = prev }()
	SetControlPlaneSnapshotStatusReader(func(_ context.Context, _, snapshotID string) (string, string, error) {
		id := uuid.MustParse(snapshotID)
		if phases[id] == "failed" {
			return "failed", "boom", nil
		}
		return phases[id], "", nil
	})

	reconcileControlPlaneSnapshots(context.Background(), ControlPlaneSnapshotSweepDeps{Queries: q})

	if len(q.succeeded) != 1 || q.succeeded[0] != succeededID {
		t.Fatalf("expected only %s marked succeeded, got %v", succeededID, q.succeeded)
	}
	if got := q.failed[failedID]; got != "boom" {
		t.Fatalf("expected failed row to carry detail 'boom', got %q", got)
	}
	if _, ok := q.failed[goneID]; !ok {
		t.Fatalf("expected 'gone' row to be failed-closed")
	}
	if _, ok := q.failed[runningID]; ok {
		t.Fatalf("running row must be left untouched")
	}
}

// fakeCPSLiveQuerier models a real DB for ListRunningControlPlaneSnapshots:
// the "running" filter is re-evaluated on every call and honours LIMIT/OFFSET,
// so marking a row terminal actually shrinks the result set — exactly the
// condition that made the old OFFSET-paginating reconcile skip still-running
// rows. It records every OFFSET it is asked for so the test can prove the
// reconcile never offset-paginates a set it mutates.
type fakeCPSLiveQuerier struct {
	fakeCPSQuerier
	order      []uuid.UUID
	status     map[uuid.UUID]string // "running" or a terminal phase
	offsetsAsk []int32
}

func (f *fakeCPSLiveQuerier) ListRunningControlPlaneSnapshots(_ context.Context, arg sqlc.ListRunningControlPlaneSnapshotsParams) ([]sqlc.ControlPlaneSnapshot, error) {
	f.offsetsAsk = append(f.offsetsAsk, arg.Offset)
	var running []sqlc.ControlPlaneSnapshot
	for _, id := range f.order {
		if f.status[id] == "running" {
			running = append(running, sqlc.ControlPlaneSnapshot{ID: id, ClusterID: uuid.New()})
		}
	}
	lo := int(arg.Offset)
	if lo > len(running) {
		lo = len(running)
	}
	hi := lo + int(arg.Limit)
	if hi > len(running) {
		hi = len(running)
	}
	return running[lo:hi], nil
}

func (f *fakeCPSLiveQuerier) MarkControlPlaneSnapshotSucceeded(_ context.Context, arg sqlc.MarkControlPlaneSnapshotSucceededParams) error {
	f.status[arg.ID] = "succeeded"
	f.succeeded = append(f.succeeded, arg.ID)
	return nil
}

func (f *fakeCPSLiveQuerier) MarkControlPlaneSnapshotFailed(_ context.Context, arg sqlc.MarkControlPlaneSnapshotFailedParams) error {
	f.status[arg.ID] = "failed"
	if f.failed == nil {
		f.failed = map[uuid.UUID]string{}
	}
	f.failed[arg.ID] = arg.Error
	return nil
}

// TestReconcileControlPlaneSnapshots_NoOffsetPaginationSkips is the F41
// regression. With MORE than one page of running rows, the reconcile marks the
// first page terminal (shrinking the "running" set). The old code advanced
// OFFSET by a page after that mutation, so rows that shifted into the slots it
// had already walked past were skipped. The fix fetches a single OFFSET-0 page
// per tick, so across two ticks every running row is reconciled and no OFFSET>0
// is ever requested against the mutating set.
func TestReconcileControlPlaneSnapshots_NoOffsetPaginationSkips(t *testing.T) {
	total := int32(controlPlaneSnapshotReconcilePageSize) + 50 // > one page
	q := &fakeCPSLiveQuerier{status: map[uuid.UUID]string{}}
	for i := int32(0); i < total; i++ {
		id := uuid.New()
		q.order = append(q.order, id)
		q.status[id] = "running"
	}

	prev := controlPlaneSnapshotStatusReader
	defer func() { controlPlaneSnapshotStatusReader = prev }()
	SetControlPlaneSnapshotStatusReader(func(_ context.Context, _, _ string) (string, string, error) {
		return "succeeded", "", nil
	})

	deps := ControlPlaneSnapshotSweepDeps{Queries: q}

	// Two ticks: page one, then the remainder.
	reconcileControlPlaneSnapshots(context.Background(), deps)
	reconcileControlPlaneSnapshots(context.Background(), deps)

	// The mutating set must never be offset-paginated.
	for _, off := range q.offsetsAsk {
		if off != 0 {
			t.Fatalf("reconcile requested OFFSET=%d against a mutating set; must always page from OFFSET 0", off)
		}
	}

	// Every row must have been reconciled across the two ticks — none skipped.
	for _, id := range q.order {
		if q.status[id] != "succeeded" {
			t.Fatalf("row %s left in status %q after two ticks; a running row was skipped", id, q.status[id])
		}
	}
}
