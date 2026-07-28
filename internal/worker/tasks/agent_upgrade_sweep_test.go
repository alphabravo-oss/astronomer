package tasks

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type agentUpgradeSweepQuerier struct {
	RuntimeQuerier
	calls []sqlc.FailStuckAgentUpgradeOperationsParams
	rows  []sqlc.AgentLifecycleOperation
	err   error
}

func (q *agentUpgradeSweepQuerier) FailStuckAgentUpgradeOperations(_ context.Context, arg sqlc.FailStuckAgentUpgradeOperationsParams) ([]sqlc.AgentLifecycleOperation, error) {
	q.calls = append(q.calls, arg)
	if q.err != nil {
		return nil, q.err
	}
	return q.rows, nil
}

// Pre-fix behaviour: an upgrade ack completed the operation immediately, so
// nothing could ever be stuck and no sweeper existed. Now the success edge is a
// heartbeat from the replacement agent — which never arrives when the cluster
// goes dark — so an unswept operation would sit in `running` forever and a
// batched fleet rollout would keep marching against a dead cluster.
func TestAgentUpgradeStuckSweep_FailsOperationsPastTheDeadline(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	q := &agentUpgradeSweepQuerier{rows: []sqlc.AgentLifecycleOperation{{
		ID:            uuid.New(),
		ClusterID:     uuid.New(),
		OperationType: "agent_upgrade",
		Status:        "failed",
		TargetVersion: "v1.2.3",
		TargetImage:   "example.com/astronomer-agent:v1.2.3",
	}}}
	runtimeDeps = RuntimeDependencies{Queries: q}

	if err := HandleAgentUpgradeStuckSweep(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(q.calls) != 1 {
		t.Fatalf("sweep calls = %d, want 1", len(q.calls))
	}
	if got := q.calls[0].StuckAfterSeconds; got != int32(defaultAgentUpgradeStuckAfter/time.Second) {
		t.Fatalf("stuck-after = %ds, want %s", got, defaultAgentUpgradeStuckAfter)
	}
	// The deadline must outlast the agent's own rollout timeout plus the
	// watchdog's rollback and the rolled-back agent's reconnect, so a rollback
	// that WORKED reports its precise reason instead of losing to this sweep.
	if defaultAgentUpgradeStuckAfter <= 10*time.Minute {
		t.Fatalf("stuck-after %s is too short to let an in-cluster rollback report first", defaultAgentUpgradeStuckAfter)
	}
	if !strings.Contains(q.calls[0].LastError, "did not report the target version") {
		t.Fatalf("last error = %q", q.calls[0].LastError)
	}
}

func TestAgentUpgradeStuckSweep_PropagatesQueryFailure(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	runtimeDeps = RuntimeDependencies{Queries: &agentUpgradeSweepQuerier{err: errors.New("boom")}}
	if err := HandleAgentUpgradeStuckSweep(context.Background(), &asynq.Task{}); err == nil {
		t.Fatal("handle returned nil, want the query error surfaced for asynq retry")
	}
}

func TestAgentUpgradeStuckSweep_SkipsWhenRuntimeUnconfigured(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	runtimeDeps = RuntimeDependencies{}
	if err := HandleAgentUpgradeStuckSweep(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("handle: %v", err)
	}
}
