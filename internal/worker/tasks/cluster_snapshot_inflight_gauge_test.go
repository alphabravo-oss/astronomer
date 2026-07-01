package tasks

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// TestPoller_MaintainsInFlightGauge is the regression for the dead
// cluster_snapshots_in_flight gauge: the poller must set a per-cluster gauge
// equal to the number of non-terminal rows it observes, and must reset a
// cluster to 0 once it drains — otherwise a stuck Velero (or a drained one)
// is invisible in the DR dashboard.
func TestPoller_MaintainsInFlightGauge(t *testing.T) {
	var mu sync.Mutex
	gauges := map[string]float64{}
	SetInFlightSnapshotGaugeSetter(func(cid string, count float64) {
		mu.Lock()
		defer mu.Unlock()
		gauges[cid] = count
	})
	t.Cleanup(func() {
		SetInFlightSnapshotGaugeSetter(func(string, float64) {})
		inFlightGaugeMu.Lock()
		inFlightGaugeSeen = map[string]struct{}{}
		inFlightGaugeMu.Unlock()
	})

	q := newFakePollQuerier()
	d := newFakeDriver()
	clusterA := uuid.New()
	clusterB := uuid.New()

	// Two in-flight snapshots for A, one for B. The driver keeps them
	// InProgress so they stay non-terminal across ticks.
	var aRows []uuid.UUID
	for _, name := range []string{"a1", "a2"} {
		row, _ := q.CreateClusterSnapshot(context.Background(), sqlc.CreateClusterSnapshotParams{
			ClusterID: clusterA, VeleroName: name, Phase: "InProgress",
		})
		aRows = append(aRows, row.ID)
		d.backupStatus[name] = VeleroBackupStatusSnapshot{Phase: "InProgress"}
	}
	q.CreateClusterSnapshot(context.Background(), sqlc.CreateClusterSnapshotParams{
		ClusterID: clusterB, VeleroName: "b1", Phase: "InProgress",
	})
	d.backupStatus["b1"] = VeleroBackupStatusSnapshot{Phase: "InProgress"}

	ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{Queries: q, Driver: d})
	defer ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{})
	SetSnapshotOutcomeRecorder(func(_, _ string) {})

	if err := HandleClusterSnapshotPoll(context.Background(), nil); err != nil {
		t.Fatalf("poll tick 1: %v", err)
	}
	mu.Lock()
	if gauges[clusterA.String()] != 2 {
		mu.Unlock()
		t.Fatalf("cluster A in-flight gauge = %v, want 2", gauges[clusterA.String()])
	}
	if gauges[clusterB.String()] != 1 {
		mu.Unlock()
		t.Fatalf("cluster B in-flight gauge = %v, want 1", gauges[clusterB.String()])
	}
	mu.Unlock()

	// Drain cluster A; B still has one pending. The next tick must reset A's
	// gauge to 0 even though A no longer appears in the pending list.
	for _, id := range aRows {
		if err := q.DeleteClusterSnapshot(context.Background(), id); err != nil {
			t.Fatalf("delete: %v", err)
		}
	}
	if err := HandleClusterSnapshotPoll(context.Background(), nil); err != nil {
		t.Fatalf("poll tick 2: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gauges[clusterA.String()] != 0 {
		t.Fatalf("cluster A gauge = %v after draining, want 0", gauges[clusterA.String()])
	}
	if gauges[clusterB.String()] != 1 {
		t.Fatalf("cluster B gauge = %v, want 1 (unaffected by A draining)", gauges[clusterB.String()])
	}
}
