package tasks

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// countingFleetQuerier counts the fleet-list and per-cluster health reads so a
// test can prove the hoist collapses G×C+G queries to O(C) per tick. Any other
// querier method nil-derefs, flagging an unexpected query path.
type countingFleetQuerier struct {
	RuntimeQuerier
	clusters    []sqlc.Cluster
	listCalls   int
	healthCalls int
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

// With the once-per-tick fleet+health hoist, evaluating G global rules over C
// clusters must issue the fleet list once and GetClusterHealthStatus exactly C
// times — NOT G full-fleet scans and G×C health point reads as before.
func TestEvaluateRule_GlobalRulesShareFleetSnapshot(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	const c = 4
	clusters := make([]sqlc.Cluster, c)
	for i := range clusters {
		clusters[i] = sqlc.Cluster{ID: uuid.New(), Name: "c", Status: "active"}
	}
	q := &countingFleetQuerier{clusters: clusters}
	runtimeDeps = RuntimeDependencies{Queries: q}

	// Build the shared snapshot once (as HandleAlertEvaluation does per tick).
	fleet, err := buildFleetHealthSnapshot(context.Background())
	if err != nil {
		t.Fatalf("buildFleetHealthSnapshot: %v", err)
	}

	// Evaluate several global rules against the SHARED snapshot.
	const g = 5
	for i := 0; i < g; i++ {
		rule := sqlc.AlertRule{ID: uuid.New(), Enabled: true, Configuration: []byte("{}")} // ClusterID zero => global
		evals, err := evaluateRule(context.Background(), rule, fleet)
		if err != nil {
			t.Fatalf("evaluateRule: %v", err)
		}
		if len(evals) != c {
			t.Fatalf("global rule produced %d evaluations, want one per cluster (%d)", len(evals), c)
		}
	}

	if q.healthCalls != c {
		t.Fatalf("GetClusterHealthStatus called %d times, want %d (once per cluster per tick, not G×C=%d)", q.healthCalls, c, g*c)
	}
	if q.listCalls != 1 {
		t.Fatalf("ListClusters called %d times, want 1 (single fleet scan per tick, not G=%d)", q.listCalls, g)
	}
}
