package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"
)

// MetricsAggregationPayload contains parameters for metrics aggregation.
type MetricsAggregationPayload struct {
	ClusterID string `json:"cluster_id,omitempty"` // empty = aggregate all
}

// NewMetricsAggregationTask creates a new metrics aggregation task.
func NewMetricsAggregationTask(payload MetricsAggregationPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal metrics aggregation payload: %w", err)
	}
	return asynq.NewTask("metrics:aggregate", data), nil
}

// HandleMetricsAggregation aggregates metrics from cluster health data.
func HandleMetricsAggregation(ctx context.Context, t *asynq.Task) error {
	var p MetricsAggregationPayload
	if len(t.Payload()) > 0 {
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal metrics aggregation payload: %w", err)
		}
	}

	if p.ClusterID != "" {
		slog.InfoContext(ctx, "aggregating metrics for cluster", "cluster_id", p.ClusterID)
	} else {
		slog.InfoContext(ctx, "aggregating metrics for all clusters")
	}

	// TODO: Read recent health check results, compute aggregates, store in metrics tables.

	slog.InfoContext(ctx, "metrics aggregation complete")
	return nil
}
