package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"
)

// HealthCheckPayload contains parameters for a health check task.
type HealthCheckPayload struct {
	ClusterID string `json:"cluster_id,omitempty"` // empty = check all clusters
}

// NewHealthCheckTask creates a new health check task with the given payload.
func NewHealthCheckTask(payload HealthCheckPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal health check payload: %w", err)
	}
	return asynq.NewTask("cluster:health_check", data), nil
}

// HandleHealthCheck checks all active cluster connections and updates health status.
func HandleHealthCheck(ctx context.Context, t *asynq.Task) error {
	var p HealthCheckPayload
	if len(t.Payload()) > 0 {
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal health check payload: %w", err)
		}
	}

	if p.ClusterID != "" {
		slog.InfoContext(ctx, "running health check for cluster", "cluster_id", p.ClusterID)
	} else {
		slog.InfoContext(ctx, "running health check for all clusters")
	}

	// TODO: Iterate active clusters, check connectivity via tunnel, update health status in DB.

	slog.InfoContext(ctx, "health check complete")
	return nil
}
