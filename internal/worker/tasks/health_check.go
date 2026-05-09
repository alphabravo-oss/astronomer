package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
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

	if runtimeDeps.Queries == nil {
		slog.InfoContext(ctx, "health check runtime not configured, skipping DB updates")
		return nil
	}

	clusters, err := healthCheckTargets(ctx, p.ClusterID)
	if err != nil {
		return err
	}
	for _, cluster := range clusters {
		if err := updateClusterHealth(ctx, cluster); err != nil {
			return err
		}
	}
	slog.InfoContext(ctx, "health check complete")
	return nil
}

func healthCheckTargets(ctx context.Context, clusterID string) ([]sqlc.Cluster, error) {
	if clusterID != "" {
		id, err := uuid.Parse(clusterID)
		if err != nil {
			return nil, fmt.Errorf("invalid cluster_id: %w", err)
		}
		cluster, err := runtimeDeps.Queries.GetClusterByID(ctx, id)
		if err != nil {
			return nil, err
		}
		return []sqlc.Cluster{cluster}, nil
	}
	return runtimeDeps.Queries.ListClusters(ctx, sqlc.ListClustersParams{Limit: 500, Offset: 0})
}

func updateClusterHealth(ctx context.Context, cluster sqlc.Cluster) error {
	status := "disconnected"
	connected := false
	if cluster.LastHeartbeat.Valid && time.Since(cluster.LastHeartbeat.Time) <= 2*time.Minute {
		status = "active"
		connected = true
	}
	if err := runtimeDeps.Queries.UpdateClusterStatus(ctx, sqlc.UpdateClusterStatusParams{
		ID:     cluster.ID,
		Status: status,
	}); err != nil {
		return err
	}

	conditions, _ := json.Marshal(map[string]any{
		"connected":          connected,
		"last_heartbeat":     cluster.LastHeartbeat.Time,
		"kubernetes_version": cluster.KubernetesVersion,
		"distribution":       cluster.Distribution,
		"source":             "worker-health-check",
	})
	_, err := runtimeDeps.Queries.UpsertClusterHealthStatus(ctx, sqlc.UpsertClusterHealthStatusParams{
		ClusterID:          cluster.ID,
		CpuUsagePercent:    0,
		MemoryUsagePercent: 0,
		PodCount:           0,
		NodeCount:          cluster.NodeCount,
		Conditions:         conditions,
	})
	return err
}
