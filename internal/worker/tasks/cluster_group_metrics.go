package tasks

import (
	"context"

	"github.com/hibiken/asynq"
)

// ClusterGroupMetricsRefreshType is the periodic task identifier for
// the gauge refresh helper introduced in sprint 066. Cron cadence is
// 5m — group membership doesn't churn fast enough to need tighter
// granularity, and the underlying CTE walks the entire tree on every
// pass.
const ClusterGroupMetricsRefreshType = "cluster_groups:refresh_metrics"

// ClusterGroupMetricsRefresher is the in-process hook the handler
// package registers at startup. Defined as a package-level function
// pointer instead of an interface to avoid an import cycle
// (handler -> worker/tasks -> handler).
var ClusterGroupMetricsRefresher func(ctx context.Context)

// NewClusterGroupMetricsRefreshTask returns a fresh task envelope for
// the scheduler.
func NewClusterGroupMetricsRefreshTask() *asynq.Task {
	return asynq.NewTask(ClusterGroupMetricsRefreshType, nil, asynq.MaxRetry(1))
}

// HandleClusterGroupMetricsRefresh is the asynq handler. Calls the
// installed refresh hook when one is registered; otherwise it's a no-op
// so tests + agent worktrees without the handler package wired don't
// fail.
func HandleClusterGroupMetricsRefresh(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, ClusterGroupMetricsRefreshType, func() error {
		if ClusterGroupMetricsRefresher == nil {
			return nil
		}
		ClusterGroupMetricsRefresher(ctx)
		return nil
	})
}
