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

// Condition types written by the worker's health check. Probe-based
// conditions are owned by the server reconciler in
// internal/server/cluster_probes.go.
const (
	ConditionConnected = "Connected"
)

// Tri-state matching metav1.ConditionStatus.
const (
	conditionTrue    = "True"
	conditionFalse   = "False"
	conditionUnknown = "Unknown"
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
	return runPeriodicTaskWithLeader(ctx, "cluster:health_check", func() error {
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
	})
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
	// 2m MUST match the metrics publisher's staleHeartbeatThreshold
	// (internal/metrics/publisher.go): both write clusters.status from
	// last_heartbeat age, and a DIFFERENT threshold makes the two fight and flap
	// the status active<->disconnected (M3). The publisher is the authoritative
	// transition-only writer; this check is a coarser backstop on the same window.
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
	if err != nil {
		return err
	}

	// Refresh per-cluster Kubernetes-style conditions. We compute these from
	// heartbeat freshness + a low-cost probe through the tunnel rather than
	// from the JSON blob above so the UI can render pills like kubectl does.
	updateClusterConditions(ctx, cluster, connected)
	return nil
}

// updateClusterConditions upserts the heartbeat-derived Connected
// condition. Probe-based conditions (AgentReachable, GatewayAPISupported)
// are owned by the server process — see internal/server/cluster_probes.go
// — because they require the tunnel-backed K8sRequester which only the
// server has access to.
func updateClusterConditions(ctx context.Context, cluster sqlc.Cluster, heartbeatFresh bool) {
	upsert := func(condType, status, reason, message string) {
		_, err := runtimeDeps.Queries.UpsertClusterCondition(ctx, sqlc.UpsertClusterConditionParams{
			ClusterID: cluster.ID,
			Type:      condType,
			Status:    status,
			Reason:    reason,
			Message:   message,
		})
		if err != nil && runtimeDeps.Log != nil {
			runtimeDeps.Log.Warn("failed to upsert cluster condition",
				"cluster_id", cluster.ID.String(), "type", condType, "error", err)
		}
	}

	// Connected: derived purely from the heartbeat freshness window. True
	// when the agent's last heartbeat is within 2m, False when it isn't,
	// Unknown when no heartbeat has ever arrived.
	switch {
	case !cluster.LastHeartbeat.Valid:
		upsert(ConditionConnected, conditionUnknown, "NoHeartbeat",
			"No heartbeat has been received from the agent yet.")
	case heartbeatFresh:
		upsert(ConditionConnected, conditionTrue, "AgentHeartbeatRecent",
			fmt.Sprintf("Agent heartbeat received at %s.",
				cluster.LastHeartbeat.Time.UTC().Format(time.RFC3339)))
	default:
		upsert(ConditionConnected, conditionFalse, "AgentHeartbeatStale",
			fmt.Sprintf("Last heartbeat %s ago (threshold 2m).",
				time.Since(cluster.LastHeartbeat.Time).Round(time.Second)))
	}
}
