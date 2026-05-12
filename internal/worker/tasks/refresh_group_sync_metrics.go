package tasks

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

// RefreshGroupSyncMetricsType is the periodic task identifier.
const RefreshGroupSyncMetricsType = "auth:refresh_group_sync_metrics"

// NewRefreshGroupSyncMetricsTask returns a task that recomputes the
// auth_group_bindings gauge from the DB. Cheap; runs every 5 minutes
// alongside the rest of the periodic sweeps.
func NewRefreshGroupSyncMetricsTask() *asynq.Task {
	return asynq.NewTask(RefreshGroupSyncMetricsType, nil, asynq.MaxRetry(1))
}

// HandleRefreshGroupSyncMetrics refreshes per-scope counts so the
// Prometheus gauge reflects current state. Without this the gauge
// only moves when SSO logins fire; long-idle clusters would show stale
// values.
func HandleRefreshGroupSyncMetrics(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, RefreshGroupSyncMetricsType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "group-sync metrics: runtime not configured, skipping")
			return nil
		}
		counter, ok := runtimeDeps.Queries.(auth.GroupSyncBindingsCounter)
		if !ok {
			return fmt.Errorf("group-sync metrics: runtime querier does not implement GroupSyncBindingsCounter")
		}
		return auth.RefreshGroupSyncMetrics(ctx, counter)
	})
}
