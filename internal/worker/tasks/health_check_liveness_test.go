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
	// health is what GetClusterHealthStatus returns; updateClusterConditions
	// reads LastMetricsAt off it to classify the MetricsAvailable condition.
	health sqlc.ClusterHealthStatus
	// simulateGuardSkip makes UpdateClusterStatusOnHeartbeat report 0 rows
	// affected, emulating the H-02 guard rejecting a stale snapshot status
	// because the cluster's heartbeat changed between snapshot and write.
	simulateGuardSkip bool
}

func (q *hcLivenessQuerier) UpdateClusterStatusOnHeartbeat(_ context.Context, arg sqlc.UpdateClusterStatusOnHeartbeatParams) (int64, error) {
	q.statuses = append(q.statuses, arg.Status)
	if q.simulateGuardSkip {
		return 0, nil
	}
	return 1, nil
}

func (q *hcLivenessQuerier) UpsertClusterHealthStatus(_ context.Context, _ sqlc.UpsertClusterHealthStatusParams) (sqlc.ClusterHealthStatus, error) {
	return sqlc.ClusterHealthStatus{}, nil
}

func (q *hcLivenessQuerier) GetClusterHealthStatus(_ context.Context, _ uuid.UUID) (sqlc.ClusterHealthStatus, error) {
	return q.health, nil
}

func (q *hcLivenessQuerier) UpsertClusterCondition(_ context.Context, arg sqlc.UpsertClusterConditionParams) (sqlc.ClusterCondition, error) {
	q.conditions = append(q.conditions, arg)
	return sqlc.ClusterCondition{}, nil
}

// connectedCondition returns the recorded Connected condition (the metrics
// sweep now also records a MetricsAvailable row, so callers can't assume the
// Connected one is the only / first entry).
func (q *hcLivenessQuerier) connectedCondition(t *testing.T) sqlc.UpsertClusterConditionParams {
	t.Helper()
	for _, c := range q.conditions {
		if c.Type == ConditionConnected {
			return c
		}
	}
	t.Fatalf("no Connected condition recorded, got %+v", q.conditions)
	return sqlc.UpsertClusterConditionParams{}
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
	if c := q.connectedCondition(t); c.Status != conditionTrue {
		t.Fatalf("Connected condition = %+v, want True", c)
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
	if c := q.connectedCondition(t); c.Status != conditionFalse {
		t.Fatalf("Connected condition = %+v, want False", c)
	}
}

// TestHealthCheckToleratesGuardSkip is the H-02 race case at the task level: the
// full-fleet snapshot computed 'disconnected', but by write time the cluster has
// reconnected, so the heartbeat-guarded write matches 0 rows. updateClusterHealth
// must treat that as a no-op (no error, converge next sweep) rather than surfacing
// it or clobbering the reconnected cluster.
func TestHealthCheckToleratesGuardSkip(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	q := &hcLivenessQuerier{simulateGuardSkip: true}
	runtimeDeps = RuntimeDependencies{Queries: q}

	cluster := sqlc.Cluster{
		ID:            uuid.New(),
		LastHeartbeat: pgtype.Timestamptz{Time: time.Now().Add(-3 * time.Minute), Valid: true},
	}
	if err := updateClusterHealth(context.Background(), cluster); err != nil {
		t.Fatalf("guard-skipped write must not error, got %v", err)
	}
	// The requested status was still 'disconnected' (snapshot value); the SQL
	// guard — not the task — is what declines to apply it.
	if len(q.statuses) != 1 || q.statuses[0] != "disconnected" {
		t.Fatalf("status writes = %v, want one 'disconnected' request", q.statuses)
	}
}

// metricsCondition returns the recorded MetricsAvailable condition (or fails).
func (q *hcLivenessQuerier) metricsCondition(t *testing.T) sqlc.UpsertClusterConditionParams {
	t.Helper()
	for _, c := range q.conditions {
		if c.Type == ConditionMetricsAvailable {
			return c
		}
	}
	t.Fatalf("no MetricsAvailable condition recorded, got %+v", q.conditions)
	return sqlc.UpsertClusterConditionParams{}
}

// runMetricsConditionCase drives updateClusterHealth for a cluster whose
// heartbeat is fresh (so it stays 'active') with the given last_metrics_at, and
// returns the recorded querier for assertions.
func runMetricsConditionCase(t *testing.T, lastMetricsAt pgtype.Timestamptz) *hcLivenessQuerier {
	t.Helper()
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	q := &hcLivenessQuerier{health: sqlc.ClusterHealthStatus{LastMetricsAt: lastMetricsAt}}
	runtimeDeps = RuntimeDependencies{Queries: q}

	cluster := sqlc.Cluster{
		ID:            uuid.New(),
		LastHeartbeat: pgtype.Timestamptz{Time: time.Now().Add(-30 * time.Second), Valid: true},
	}
	if err := updateClusterHealth(context.Background(), cluster); err != nil {
		t.Fatalf("updateClusterHealth returned error: %v", err)
	}
	return q
}

// TestMetricsConditionFlowing (matrix a): metrics-server present + flowing →
// MetricsFlowing(True), and the cluster stays 'active' (metrics never touch
// liveness).
func TestMetricsConditionFlowing(t *testing.T) {
	q := runMetricsConditionCase(t, pgtype.Timestamptz{Time: time.Now().Add(-30 * time.Second), Valid: true})
	if q.statuses[0] != "active" {
		t.Fatalf("status = %v, want active (metrics-flowing must not affect liveness)", q.statuses)
	}
	c := q.metricsCondition(t)
	if c.Status != conditionTrue || c.Reason != "MetricsFlowing" {
		t.Fatalf("MetricsAvailable = %+v, want True/MetricsFlowing", c)
	}
}

// TestMetricsConditionStale (matrix b): metrics-server present but sample old →
// MetricsStale(False), distinct reason, cluster STILL active. A metrics-stale
// cluster must never flip clusters.status.
func TestMetricsConditionStale(t *testing.T) {
	q := runMetricsConditionCase(t, pgtype.Timestamptz{Time: time.Now().Add(-10 * time.Minute), Valid: true})
	if q.statuses[0] != "active" {
		t.Fatalf("status = %v, want active (metrics-stale must NOT flip liveness)", q.statuses)
	}
	c := q.metricsCondition(t)
	if c.Status != conditionFalse || c.Reason != "MetricsStale" {
		t.Fatalf("MetricsAvailable = %+v, want False/MetricsStale", c)
	}
}

// TestMetricsConditionNoMetricsServer (matrix c): no sample ever (NULL
// last_metrics_at) → NoMetricsServer(False), DISTINCT from MetricsStale, and
// the cluster stays active with no error.
func TestMetricsConditionNoMetricsServer(t *testing.T) {
	q := runMetricsConditionCase(t, pgtype.Timestamptz{Valid: false})
	if q.statuses[0] != "active" {
		t.Fatalf("status = %v, want active (no-metrics-server must not affect liveness)", q.statuses)
	}
	c := q.metricsCondition(t)
	if c.Status != conditionFalse || c.Reason != "NoMetricsServer" {
		t.Fatalf("MetricsAvailable = %+v, want False/NoMetricsServer", c)
	}
	// Distinct from the stale reason — the whole point of M13.
	if c.Reason == "MetricsStale" {
		t.Fatalf("NoMetricsServer must be distinguishable from MetricsStale")
	}
}
