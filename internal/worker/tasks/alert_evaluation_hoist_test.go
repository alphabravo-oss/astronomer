package tasks

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// countingFleetQuerier counts fleet-list, point-read, and batch health calls so
// a test can prove the hoist collapses G×C+G queries to O(pages) per tick. Any other
// querier method nil-derefs, flagging an unexpected query path.
type countingFleetQuerier struct {
	RuntimeQuerier
	clusters      []sqlc.Cluster
	listCalls     int
	clusterBatch  int
	clusterPoint  int
	healthCalls   int
	batchCalls    int
	livenessCalls int
	livenessPoint int
}

func (q *countingFleetQuerier) ListClustersByIDs(_ context.Context, _ []uuid.UUID) ([]sqlc.Cluster, error) {
	q.clusterBatch++
	return q.clusters, nil
}

func (q *countingFleetQuerier) GetClusterByID(_ context.Context, _ uuid.UUID) (sqlc.Cluster, error) {
	q.clusterPoint++
	return sqlc.Cluster{}, nil
}

func (q *countingFleetQuerier) GetClusterLiveness(_ context.Context, _ uuid.UUID) (sqlc.ClusterLiveness, error) {
	q.livenessPoint++
	return sqlc.ClusterLiveness{}, nil
}

func (q *countingFleetQuerier) ListClusterLivenessForClusters(_ context.Context, ids []uuid.UUID) ([]sqlc.ClusterLiveness, error) {
	q.livenessCalls++
	rows := make([]sqlc.ClusterLiveness, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, sqlc.ClusterLiveness{ClusterID: id})
	}
	return rows, nil
}

func (q *countingFleetQuerier) ListClusters(_ context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	q.listCalls++
	if arg.Offset > 0 {
		return nil, nil
	}
	return q.clusters, nil
}

func (q *countingFleetQuerier) GetClusterHealthStatus(_ context.Context, _ uuid.UUID) (sqlc.ClusterHealthStatus, error) {
	q.healthCalls++
	return sqlc.ClusterHealthStatus{}, nil
}

func (q *countingFleetQuerier) ListClusterHealthStatusesForClusters(_ context.Context, ids []uuid.UUID) ([]sqlc.ClusterHealthStatus, error) {
	q.batchCalls++
	rows := make([]sqlc.ClusterHealthStatus, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, sqlc.ClusterHealthStatus{ClusterID: id})
	}
	return rows, nil
}

// With the once-per-tick fleet+health hoist, evaluating G global rules over C
// clusters must issue one fleet list and one batch health read for this page —
// no point lookups and no G×C query fan-out.
func TestEvaluateRule_GlobalRulesShareFleetSnapshot(t *testing.T) {
	const c = 4
	clusters := make([]sqlc.Cluster, c)
	for i := range clusters {
		clusters[i] = sqlc.Cluster{ID: uuid.New(), Name: "c", Status: "active"}
	}
	q := &countingFleetQuerier{clusters: clusters}
	ctx := testRuntimeContext(RuntimeDependencies{Queries: q})

	// Build the shared snapshot once (as HandleAlertEvaluation does per tick).
	fleet, err := buildFleetHealthSnapshot(ctx)
	if err != nil {
		t.Fatalf("buildFleetHealthSnapshot: %v", err)
	}

	// Evaluate several global rules against the SHARED snapshot.
	const g = 5
	for i := 0; i < g; i++ {
		rule := sqlc.AlertRule{ID: uuid.New(), Enabled: true, Configuration: []byte("{}")} // ClusterID zero => global
		evals, err := evaluateRule(ctx, rule, fleet)
		if err != nil {
			t.Fatalf("evaluateRule: %v", err)
		}
		if len(evals) != c {
			t.Fatalf("global rule produced %d evaluations, want one per cluster (%d)", len(evals), c)
		}
	}

	if q.healthCalls != 0 {
		t.Fatalf("GetClusterHealthStatus called %d times, want 0 (fleet health must be batched)", q.healthCalls)
	}
	if q.batchCalls != 1 {
		t.Fatalf("ListClusterHealthStatusesForClusters called %d times, want 1 per fleet page", q.batchCalls)
	}
	if q.livenessCalls != 1 {
		t.Fatalf("ListClusterLivenessForClusters called %d times, want 1 per fleet page", q.livenessCalls)
	}
	if q.listCalls != 1 {
		t.Fatalf("ListClusters called %d times, want 1 (single fleet scan per tick, not G=%d)", q.listCalls, g)
	}
}

func TestEvaluateRule_ClusterScopedRulesShareBatchedSnapshot(t *testing.T) {
	clusterID := uuid.New()
	q := &countingFleetQuerier{clusters: []sqlc.Cluster{{ID: clusterID, Name: "scoped", Status: "connected"}}}
	ctx := testRuntimeContext(RuntimeDependencies{Queries: q})
	snapshot, err := buildScopedHealthSnapshot(ctx, []uuid.UUID{clusterID, clusterID})
	if err != nil {
		t.Fatalf("buildScopedHealthSnapshot: %v", err)
	}
	for i := 0; i < 2; i++ {
		rule := sqlc.AlertRule{
			ID:            uuid.New(),
			Enabled:       true,
			ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
			Configuration: []byte(`{}`),
		}
		if _, err := evaluateRule(ctx, rule, snapshot); err != nil {
			t.Fatalf("evaluate scoped rule: %v", err)
		}
	}
	if q.clusterBatch != 1 || q.batchCalls != 1 || q.livenessCalls != 1 {
		t.Fatalf("batch calls cluster/health/liveness = %d/%d/%d, want 1/1/1", q.clusterBatch, q.batchCalls, q.livenessCalls)
	}
	if q.clusterPoint != 0 || q.healthCalls != 0 || q.livenessPoint != 0 {
		t.Fatalf("point calls cluster/health/liveness = %d/%d/%d, want 0/0/0", q.clusterPoint, q.healthCalls, q.livenessPoint)
	}
}
