// Migration 055 — nightly recompute of chart rating aggregates and the
// co-installation matrix. The handler-side per-write recompute keeps
// the aggregates fresh on the hot path; this sweep is the backstop for
// catalog churn (charts removed, ratings deleted by user-account
// cascade, etc.) and the only path that updates chart_co_installation.

package tasks

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
)

// ChartRecommendationsRecomputeType is the periodic task identifier.
const ChartRecommendationsRecomputeType = "chart_recommendations:recompute"

// NewChartRecommendationsRecomputeTask is the constructor used by tests
// that exercise the asynq mux directly. The scheduler invokes via cron.
func NewChartRecommendationsRecomputeTask() *asynq.Task {
	return asynq.NewTask(ChartRecommendationsRecomputeType, nil, asynq.MaxRetry(2))
}

// chartRecommendationsQuerier is the sub-interface the recompute task
// needs out of the runtime querier. We assert on this at task entry so
// a non-conforming querier fails fast with a clear message rather than
// nil-panicking deep inside the recompute helpers.
type chartRecommendationsQuerier interface {
	catalog.Querier
}

// HandleChartRecommendationsRecompute runs the nightly recompute:
//  1. RecomputeCoInstallation — rebuilds chart_co_installation.
//  2. RecomputeAllAggregates — recomputes every rated chart's
//     aggregate row.
//
// Order matters: a chart that was removed from the catalog still has
// a chart_ratings row until ON DELETE CASCADE fires (which the FK
// guarantees) — by recomputing aggregates *after* the matrix rebuild,
// we ensure the popular-list ordering reflects the most recent matrix
// state.
func HandleChartRecommendationsRecompute(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, ChartRecommendationsRecomputeType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "chart recommendations runtime not configured, skipping")
			return nil
		}
		q, ok := runtimeDeps.Queries.(chartRecommendationsQuerier)
		if !ok {
			return fmt.Errorf("chart recommendations not supported by runtime querier")
		}
		if err := catalog.RecomputeCoInstallation(ctx, q); err != nil {
			return fmt.Errorf("recompute co-installation: %w", err)
		}
		if err := catalog.RecomputeAllAggregates(ctx, q); err != nil {
			return fmt.Errorf("recompute aggregates: %w", err)
		}
		runtimeLogger().InfoContext(ctx, "chart recommendations recompute completed")
		return nil
	})
}
