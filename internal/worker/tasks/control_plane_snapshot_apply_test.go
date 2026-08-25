package tasks

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestControlPlaneSnapshotApplyReplaysIdempotentIntent(t *testing.T) {
	clusterID, snapshotID := uuid.New(), uuid.New()
	q := &fakeCPSQuerier{
		clusters: map[uuid.UUID]sqlc.Cluster{
			clusterID: {ID: clusterID, Name: "production", Distribution: "rke2"},
		},
		snapshots: map[uuid.UUID]sqlc.ControlPlaneSnapshot{
			snapshotID: {ID: snapshotID, ClusterID: clusterID, Name: "cp-one", Location: "s3", Status: "pending"},
		},
	}
	var applied []string
	runtime := ControlPlaneSnapshotRuntime{
		Deps: ControlPlaneSnapshotSweepDeps{Queries: q},
		Applier: func(_ context.Context, cluster, snapshot, name, family, location string) error {
			applied = append(applied, strings.Join([]string{cluster, snapshot, name, family, location}, ":"))
			return nil
		},
	}
	task, err := NewControlPlaneSnapshotApplyTask(snapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.HandleControlPlaneSnapshotApply(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || len(q.statuses) != 1 || q.statuses[0].Status != "running" {
		t.Fatalf("applied=%v statuses=%+v", applied, q.statuses)
	}
	// A replay after the first success observes running and performs no second
	// remote mutation.
	if err := runtime.HandleControlPlaneSnapshotApply(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 {
		t.Fatalf("replay applied privileged Job %d times", len(applied))
	}
}

func TestControlPlaneSnapshotApplyReturnsTransientFailureAndStoresSanitizedState(t *testing.T) {
	clusterID, snapshotID := uuid.New(), uuid.New()
	q := &fakeCPSQuerier{
		clusters: map[uuid.UUID]sqlc.Cluster{clusterID: {ID: clusterID, Distribution: "k3s"}},
		snapshots: map[uuid.UUID]sqlc.ControlPlaneSnapshot{
			snapshotID: {ID: snapshotID, ClusterID: clusterID, Name: "cp-one", Location: "local", Status: "pending"},
		},
	}
	runtime := ControlPlaneSnapshotRuntime{
		Deps: ControlPlaneSnapshotSweepDeps{Queries: q},
		Applier: func(context.Context, string, string, string, string, string) error {
			return errors.New("token-SENTINEL from upstream")
		},
	}
	task, _ := NewControlPlaneSnapshotApplyTask(snapshotID)
	err := runtime.HandleControlPlaneSnapshotApply(context.Background(), task)
	if err == nil || !strings.Contains(err.Error(), "token-SENTINEL") {
		t.Fatalf("transient failure not returned to Asynq: %v", err)
	}
	if len(q.statuses) != 1 || q.statuses[0].Status != "pending" || strings.Contains(q.statuses[0].Error, "SENTINEL") {
		t.Fatalf("persisted status was not sanitized: %+v", q.statuses)
	}
}

func TestControlPlaneSnapshotApplyTreatsMissingIntentAsNoop(t *testing.T) {
	q := &fakeCPSQuerier{clusters: map[uuid.UUID]sqlc.Cluster{}, snapshots: map[uuid.UUID]sqlc.ControlPlaneSnapshot{}}
	runtime := ControlPlaneSnapshotRuntime{
		Deps: ControlPlaneSnapshotSweepDeps{Queries: q},
		Applier: func(context.Context, string, string, string, string, string) error {
			t.Fatal("stale intent invoked applier")
			return nil
		},
	}
	task, _ := NewControlPlaneSnapshotApplyTask(uuid.New())
	if err := runtime.HandleControlPlaneSnapshotApply(context.Background(), task); err != nil {
		t.Fatalf("missing desired-state row: %v", err)
	}
}

func TestControlPlaneSnapshotSweepRepairsPendingRowAfterCrash(t *testing.T) {
	clusterID, snapshotID := uuid.New(), uuid.New()
	q := &fakeCPSQuerier{
		clusters: map[uuid.UUID]sqlc.Cluster{clusterID: {ID: clusterID, Distribution: "kubeadm"}},
		snapshots: map[uuid.UUID]sqlc.ControlPlaneSnapshot{
			snapshotID: {ID: snapshotID, ClusterID: clusterID, Name: "orphaned-pending", Location: "local", Status: "pending"},
		},
	}
	applied := 0
	runtime := ControlPlaneSnapshotRuntime{
		Deps: ControlPlaneSnapshotSweepDeps{Queries: q},
		Applier: func(context.Context, string, string, string, string, string) error {
			applied++
			return nil
		},
	}
	if err := runtime.reconcilePendingControlPlaneSnapshots(context.Background(), runtime.Deps); err != nil {
		t.Fatal(err)
	}
	if applied != 1 || q.snapshots[snapshotID].Status != "running" {
		t.Fatalf("pending repair applied=%d row=%+v", applied, q.snapshots[snapshotID])
	}
}
