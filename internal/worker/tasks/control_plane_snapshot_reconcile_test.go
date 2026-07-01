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
