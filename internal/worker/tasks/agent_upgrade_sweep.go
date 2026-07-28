package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// AgentUpgradeStuckSweepType is the periodic task identifier for failing agent
// upgrade operations whose agent never came back.
const AgentUpgradeStuckSweepType = "agent_upgrade:sweep_stuck"

// defaultAgentUpgradeStuckAfter bounds how long an upgrade may sit in `running`
// before it is declared failed.
//
// It must be comfortably longer than the agent's own rollout timeout plus the
// in-cluster watchdog's rollback and the rolled-back agent's reconnect (5m + a
// couple of minutes by default), so a rollback that WORKED reports its own
// precise reason rather than losing the race to this generic sweep. It must
// also be short enough that a batched fleet rollout does not march on for long
// against a cluster that has gone dark.
const defaultAgentUpgradeStuckAfter = 30 * time.Minute

const agentUpgradeStuckError = "agent did not report the target version within the rollout deadline; the cluster may be running the previous agent (rolled back) or no agent at all — check the astronomer-agent Deployment in the cluster"

// agentUpgradeSweeper is the optional sub-interface satisfied by the production
// *sqlc.Queries; declared locally so the RuntimeQuerier surface need not grow.
type agentUpgradeSweeper interface {
	FailStuckAgentUpgradeOperations(ctx context.Context, arg sqlc.FailStuckAgentUpgradeOperationsParams) ([]sqlc.AgentLifecycleOperation, error)
}

// NewAgentUpgradeStuckSweepTask returns the periodic task envelope.
func NewAgentUpgradeStuckSweepTask() *asynq.Task {
	return asynq.NewTask(AgentUpgradeStuckSweepType, nil, asynq.MaxRetry(2))
}

// HandleAgentUpgradeStuckSweep fails agent upgrade operations stuck in
// `running`.
//
// Why this exists: since the self-upgrade hardening, a patch ack no longer
// completes an operation (internal/tunnel/handler.go). Success is a heartbeat
// from the replacement agent. The failure edge is normally an explicit
// rolled_back result delivered by the agent the in-cluster watchdog restored —
// but if the watchdog could not run, the node died, or the cluster went dark
// entirely, no result will EVER arrive. Without this sweep such an operation
// stays `running` forever and the fleet UI would show an upgrade in flight for
// a cluster that is gone.
func HandleAgentUpgradeStuckSweep(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, AgentUpgradeStuckSweepType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().DebugContext(ctx, "agent upgrade sweep runtime not configured, skipping")
			return nil
		}
		q, ok := runtimeDeps.Queries.(agentUpgradeSweeper)
		if !ok {
			return fmt.Errorf("agent upgrade sweep not supported by runtime querier")
		}
		stuck, err := q.FailStuckAgentUpgradeOperations(ctx, sqlc.FailStuckAgentUpgradeOperationsParams{
			StuckAfterSeconds: int32(defaultAgentUpgradeStuckAfter / time.Second),
			LastError:         agentUpgradeStuckError,
		})
		if err != nil {
			return fmt.Errorf("fail stuck agent upgrade operations: %w", err)
		}
		for _, op := range stuck {
			runtimeLogger().WarnContext(ctx, "failed a stuck agent upgrade operation",
				"operation_id", op.ID.String(),
				"cluster_id", op.ClusterID.String(),
				"target_version", op.TargetVersion,
				"target_image", op.TargetImage,
			)
		}
		return nil
	})
}
