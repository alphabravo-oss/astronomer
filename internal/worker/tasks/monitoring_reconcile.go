package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
)

type MonitoringReconcilePayload struct {
	ClusterID string `json:"cluster_id,omitempty"`
}

func NewMonitoringReconcileTask(payload MonitoringReconcilePayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal monitoring reconcile payload: %w", err)
	}
	return asynq.NewTask("monitoring:reconcile", data), nil
}

func HandleMonitoringReconcile(ctx context.Context, t *asynq.Task) error {
	var p MonitoringReconcilePayload
	if len(t.Payload()) > 0 {
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal monitoring reconcile payload: %w", err)
		}
	}

	if runtimeDeps.Queries == nil {
		slog.InfoContext(ctx, "monitoring reconcile runtime not configured, skipping")
		return nil
	}

	backend, err := runtimeDeps.Queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		if err == pgx.ErrNoRows {
			slog.InfoContext(ctx, "default monitoring backend not configured, skipping reconcile")
			return nil
		}
		return fmt.Errorf("load monitoring backend: %w", err)
	}

	backend, client, backendHealthy, err := reconcileMonitoringBackend(ctx, backend)
	if err != nil {
		return err
	}
	if client == nil {
		slog.InfoContext(ctx, "monitoring backend query URL not configured, skipping cluster reconciliation")
		return nil
	}

	clusters, err := runtimeDeps.Queries.ListClusters(ctx, sqlc.ListClustersParams{Limit: 1000, Offset: 0})
	if err != nil {
		return fmt.Errorf("list clusters: %w", err)
	}

	for _, cluster := range clusters {
		if p.ClusterID != "" && cluster.ID.String() != p.ClusterID {
			continue
		}
		if err := reconcileClusterMonitoring(ctx, client, cluster, backend, backendHealthy); err != nil {
			runtimeLogger().WarnContext(ctx, "cluster monitoring reconcile failed", "cluster_id", cluster.ID.String(), "error", err)
		}
	}

	return nil
}

func reconcileMonitoringBackend(ctx context.Context, backend sqlc.MonitoringBackend) (sqlc.MonitoringBackend, *imonitoring.Client, bool, error) {
	authCfg := decodeJSONMapLocal(backend.AuthConfig)
	shared := mapFromAny(authCfg["sharedThanos"])
	status := "not_configured"
	var client *imonitoring.Client

	if backend.QueryUrl != "" {
		c, err := imonitoring.NewClient(imonitoring.BackendConfig{
			QueryURL:           backend.QueryUrl,
			TenantID:           backend.TenantID,
			AuthType:           backend.AuthType,
			AuthConfig:         backend.AuthConfig,
			DefaultStepSeconds: backend.DefaultStepSeconds,
			TimeoutSeconds:     backend.TimeoutSeconds,
		})
		if err != nil {
			status = "degraded"
		} else {
			client = c
			if err := client.HealthCheck(ctx); err != nil {
				status = "degraded"
				runtimeLogger().WarnContext(ctx, "monitoring backend health check failed", "query_url", backend.QueryUrl, "error", err)
			} else {
				status = "healthy"
			}
		}
	}

	if len(shared) > 0 {
		shared["status"] = status
		shared["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
		authCfg["sharedThanos"] = shared
		raw, err := json.Marshal(authCfg)
		if err != nil {
			return backend, client, status == "healthy", err
		}
		updated, err := runtimeDeps.Queries.UpsertDefaultMonitoringBackend(ctx, sqlc.UpsertDefaultMonitoringBackendParams{
			BackendType:        backend.BackendType,
			QueryUrl:           backend.QueryUrl,
			AlertmanagerUrl:    backend.AlertmanagerUrl,
			TenantID:           backend.TenantID,
			AuthType:           backend.AuthType,
			AuthConfig:         raw,
			DefaultStepSeconds: backend.DefaultStepSeconds,
			TimeoutSeconds:     backend.TimeoutSeconds,
			CreatedByID:        backend.CreatedByID,
		})
		if err != nil {
			return backend, client, status == "healthy", fmt.Errorf("persist monitoring backend status: %w", err)
		}
		backend = updated
	}

	return backend, client, status == "healthy", nil
}

func reconcileClusterMonitoring(ctx context.Context, client *imonitoring.Client, cluster sqlc.Cluster, backend sqlc.MonitoringBackend, backendHealthy bool) error {
	cfg, err := runtimeDeps.Queries.GetClusterMonitoringConfig(ctx, cluster.ID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	}
	if cfg.Status == "uninstalled" {
		return nil
	}

	status := "degraded"
	lastHealthyAt := pgtype.Timestamptz{}

	if backendHealthy {
		label := cfg.ClusterLabel
		if label == "" {
			label = "cluster_id"
		}
		value := cfg.ClusterLabelValue
		if value == "" {
			value = cluster.ID.String()
		}
		upCount, err := client.QueryScalar(ctx, fmt.Sprintf(`count(up{%s="%s"})`, label, escapePromLabelLocal(value)))
		if err != nil {
			runtimeLogger().WarnContext(ctx, "cluster metrics query failed", "cluster_id", cluster.ID.String(), "error", err)
		} else if upCount > 0 {
			status = "healthy"
			lastHealthyAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
		}
	}

	_, err = runtimeDeps.Queries.UpsertClusterMonitoringConfig(ctx, sqlc.UpsertClusterMonitoringConfigParams{
		ClusterID:               cfg.ClusterID,
		BackendID:               backend.ID,
		ClusterLabel:            cfg.ClusterLabel,
		ClusterLabelValue:       cfg.ClusterLabelValue,
		ScrapeIntervalSeconds:   cfg.ScrapeIntervalSeconds,
		Retention:               cfg.Retention,
		StackNamespace:          cfg.StackNamespace,
		PrometheusReleaseName:   cfg.PrometheusReleaseName,
		ThanosSidecarEnabled:    cfg.ThanosSidecarEnabled,
		StorageConfigID:         cfg.StorageConfigID,
		ObjectStorageSecretName: cfg.ObjectStorageSecretName,
		StorageClass:            cfg.StorageClass,
		StorageSize:             cfg.StorageSize,
		LastAppliedSpecHash:     cfg.LastAppliedSpecHash,
		LastObservedStatus:      cfg.LastObservedStatus,
		LastObservedRevision:    cfg.LastObservedRevision,
		LastObservedAt:          cfg.LastObservedAt,
		LastDriftDetectedAt:     cfg.LastDriftDetectedAt,
		Status:                  status,
		LastHealthyAt:           lastHealthyAt,
		CreatedByID:             cfg.CreatedByID,
	})
	return err
}

func decodeJSONMapLocal(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func mapFromAny(v any) map[string]any {
	out, _ := v.(map[string]any)
	if out == nil {
		return map[string]any{}
	}
	return out
}

func escapePromLabelLocal(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return replacer.Replace(value)
}
