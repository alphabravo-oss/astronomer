package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// AgentTokenRotateSweepType is the asynq task type for the periodic durable
// agent-token rotation policy sweep (task A2). Leader-gated so only one worker
// pod drives it. The sweep reads each cluster's registration policy
// (token_rotation_days) and sets rotation_pending_at on clusters whose token
// is older than the policy. The actual rotation happens lazily on the agent's
// next CONNECT (grace rotation in the tunnel server) — this task only marks
// clusters as due.
const AgentTokenRotateSweepType = "agent_token:rotate_sweep"

// agentTokenRotateSweepBatchLimit caps how many clusters one tick flags, so a
// large fleet that all becomes due at once doesn't get flagged in a single
// burst (the next tick picks up the remainder).
const agentTokenRotateSweepBatchLimit = 500

// agentTokenRotateGraceMinutes is the backstop after which a never-cleared
// previous_token_hash is swept away even if the agent never reconnected with
// the new token. 10 minutes is well beyond the agent's reconnect backoff.
const agentTokenRotateGraceMinutes = 10

// AgentTokenRotateQuerier is the narrow slice of *sqlc.Queries the sweep
// needs. Local interface so unit tests stand up a fake without the full
// Queries surface.
type AgentTokenRotateQuerier interface {
	ListClustersDueForAgentTokenRotation(ctx context.Context, rowLimit int32) ([]sqlc.ListClustersDueForAgentTokenRotationRow, error)
	SetClusterAgentTokenRotationPending(ctx context.Context, clusterID uuid.UUID) (int64, error)
	ClearExpiredAgentTokenRotationGrace(ctx context.Context, graceMinutes int32) (int64, error)
}

// AgentTokenRotateDeps wires the sweep. Set once at startup via
// ConfigureAgentTokenRotate; tests swap a fake.
type AgentTokenRotateDeps struct {
	Queries AgentTokenRotateQuerier
}

var agentTokenRotateDeps AgentTokenRotateDeps

// ConfigureAgentTokenRotate wires runtime dependencies. Called once from
// the server bootstrap.
func ConfigureAgentTokenRotate(deps AgentTokenRotateDeps) {
	agentTokenRotateDeps = deps
}

// ResetAgentTokenRotate clears the runtime deps. Used by tests.
func ResetAgentTokenRotate() {
	agentTokenRotateDeps = AgentTokenRotateDeps{}
}

// HandleAgentTokenRotateSweep is the asynq handler. Leader-gated through
// runPeriodicTaskWithLeader so only the lease holder drives the tick.
func HandleAgentTokenRotateSweep(ctx context.Context, _ *asynq.Task) error {
	if agentTokenRotateDeps.Queries == nil {
		runtimeLogger().InfoContext(ctx, "agent token rotate sweep runtime not configured, skipping")
		return nil
	}
	return runPeriodicTaskWithLeader(ctx, AgentTokenRotateSweepType, func() error {
		tickCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		return runAgentTokenRotateSweep(tickCtx, agentTokenRotateDeps)
	})
}

// runAgentTokenRotateSweep is the testable core. It (1) flags clusters whose
// token_rotation_days policy has elapsed by setting rotation_pending_at, and
// (2) runs the grace-TTL backstop that clears any stale previous_token_hash.
func runAgentTokenRotateSweep(ctx context.Context, deps AgentTokenRotateDeps) error {
	due, err := deps.Queries.ListClustersDueForAgentTokenRotation(ctx, agentTokenRotateSweepBatchLimit)
	if err != nil {
		return fmt.Errorf("list clusters due for rotation: %w", err)
	}
	flagged := 0
	for _, row := range due {
		if ctx.Err() != nil {
			break
		}
		n, err := deps.Queries.SetClusterAgentTokenRotationPending(ctx, row.ClusterID)
		if err != nil {
			runtimeLogger().ErrorContext(ctx, "set rotation pending", "error", err, "cluster_id", row.ClusterID)
			continue
		}
		if n > 0 {
			flagged++
		}
	}

	// Grace-TTL backstop: clear previous_token_hash rows whose rotation
	// completed more than the grace window ago but whose old hash a
	// new-token CONNECT never cleared (e.g. the agent never reconnected).
	if _, err := deps.Queries.ClearExpiredAgentTokenRotationGrace(ctx, agentTokenRotateGraceMinutes); err != nil {
		runtimeLogger().ErrorContext(ctx, "clear expired rotation grace", "error", err)
	}

	if flagged > 0 {
		runtimeLogger().InfoContext(ctx, "agent token rotation sweep flagged clusters", "count", flagged)
	}
	return nil
}
