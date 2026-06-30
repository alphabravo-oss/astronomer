package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// hcLivenessQuerier embeds RuntimeQuerier and overrides only the writes that
// updateClusterHealth performs, capturing the status decision.
type hcLivenessQuerier struct {
	RuntimeQuerier
	statuses   []string
	conditions []sqlc.UpsertClusterConditionParams
}

func (q *hcLivenessQuerier) UpdateClusterStatus(_ context.Context, arg sqlc.UpdateClusterStatusParams) error {
	q.statuses = append(q.statuses, arg.Status)
	return nil
}

func (q *hcLivenessQuerier) UpsertClusterHealthStatus(_ context.Context, _ sqlc.UpsertClusterHealthStatusParams) (sqlc.ClusterHealthStatus, error) {
	return sqlc.ClusterHealthStatus{}, nil
}

func (q *hcLivenessQuerier) UpsertClusterCondition(_ context.Context, arg sqlc.UpsertClusterConditionParams) (sqlc.ClusterCondition, error) {
	q.conditions = append(q.conditions, arg)
	return sqlc.ClusterCondition{}, nil
}

// TestHealthCheckKeepsClusterActiveOnRecentBeat proves the H11 outcome: because
// a degraded/minimal beat still advances last_heartbeat, the worker health check
// keeps the cluster 'active' (Connected=True) and does NOT flip it to
// disconnected — so cluster_condition_reconcile never mints a spurious token.
func TestHealthCheckKeepsClusterActiveOnRecentBeat(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	q := &hcLivenessQuerier{}
	runtimeDeps = RuntimeDependencies{Queries: q}

	cluster := sqlc.Cluster{
		ID: uuid.New(),
		// A degraded beat 30s ago keeps last_heartbeat fresh (inside the 2m window).
		LastHeartbeat: pgtype.Timestamptz{Time: time.Now().Add(-30 * time.Second), Valid: true},
	}
	if err := updateClusterHealth(context.Background(), cluster); err != nil {
		t.Fatalf("updateClusterHealth returned error: %v", err)
	}
	if len(q.statuses) != 1 || q.statuses[0] != "active" {
		t.Fatalf("status writes = %v, want one 'active' (recent beat must stay active)", q.statuses)
	}
	if len(q.conditions) != 1 || q.conditions[0].Type != ConditionConnected || q.conditions[0].Status != conditionTrue {
		t.Fatalf("Connected condition = %+v, want True", q.conditions)
	}
}

// TestHealthCheckFlipsGenuinelyStaleCluster proves the real-disconnect path is
// preserved: with no beats AND no pongs the tunnel is actually down, so a stale
// last_heartbeat still flips the cluster to 'disconnected' (Connected=False).
func TestHealthCheckFlipsGenuinelyStaleCluster(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	q := &hcLivenessQuerier{}
	runtimeDeps = RuntimeDependencies{Queries: q}

	cluster := sqlc.Cluster{
		ID: uuid.New(),
		// 3m old, outside the 2m window: a genuine disconnect.
		LastHeartbeat: pgtype.Timestamptz{Time: time.Now().Add(-3 * time.Minute), Valid: true},
	}
	if err := updateClusterHealth(context.Background(), cluster); err != nil {
		t.Fatalf("updateClusterHealth returned error: %v", err)
	}
	if len(q.statuses) != 1 || q.statuses[0] != "disconnected" {
		t.Fatalf("status writes = %v, want one 'disconnected' (stale cluster must flip)", q.statuses)
	}
	if len(q.conditions) != 1 || q.conditions[0].Status != conditionFalse {
		t.Fatalf("Connected condition = %+v, want False", q.conditions)
	}
}
