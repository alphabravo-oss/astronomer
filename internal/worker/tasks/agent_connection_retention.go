package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// AgentConnectionRetentionType identifies the daily terminal connection-row
// retention sweep. Connected rows are excluded by the query regardless of age.
const AgentConnectionRetentionType = "agent_connections:enforce_retention"

// AgentConnectionHistoryRetention preserves enough history for operational
// investigation while bounding reconnect-heavy installations.
const AgentConnectionHistoryRetention = 30 * 24 * time.Hour

// NewAgentConnectionRetentionTask constructs the bounded-retry periodic task.
func NewAgentConnectionRetentionTask() *asynq.Task {
	return asynq.NewTask(AgentConnectionRetentionType, nil, asynq.MaxRetry(2))
}

// HandleAgentConnectionRetention deletes terminal connection history older
// than the retention window. Leader election prevents duplicate fleet-wide
// deletes when multiple worker replicas receive the scheduled task.
func HandleAgentConnectionRetention(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, AgentConnectionRetentionType, func() error {
		q := runtimeDependencies(ctx).Queries
		if q == nil {
			return fmt.Errorf("agent connection retention runtime is not configured")
		}
		cutoff := time.Now().UTC().Add(-AgentConnectionHistoryRetention)
		pruned, err := q.PruneAgentConnectionHistoryBefore(ctx, cutoff)
		if err != nil {
			return fmt.Errorf("prune agent connection history: %w", err)
		}
		runtimeLogger(ctx).InfoContext(ctx, "pruned agent connection history",
			"rows", pruned,
			"cutoff", cutoff.Format(time.RFC3339),
			"retention", AgentConnectionHistoryRetention.String(),
		)
		return nil
	})
}
