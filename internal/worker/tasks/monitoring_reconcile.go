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
	return runPeriodicTaskWithLeader(ctx, "monitoring:reconcile", func() error {
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

		clusters, err := listAllClustersPaged(ctx, runtimeDeps.Queries.ListClusters)
		if err != nil {
			return fmt.Errorf("list clusters: %w", err)
		}

		// PERF-01/02: page full fleet + fan-out so 100–500+ clusters stay
		// within the reconcile tick (serial loop previously overran).
		if p.ClusterID != "" {
			for _, cluster := range clusters {
				if cluster.ID.String() != p.ClusterID {
					continue
				}
				if err := reconcileClusterMonitoring(ctx, client, cluster, backend, backendHealthy); err != nil {
					runtimeLogger().WarnContext(ctx, "cluster monitoring reconcile failed", "cluster_id", cluster.ID.String(), "error", err)
				}
			}
			return nil
		}
		fanOutClusters(ctx, clusters, 30*time.Second, func(ctx context.Context, cluster sqlc.Cluster) {
			if err := reconcileClusterMonitoring(ctx, client, cluster, backend, backendHealthy); err != nil {
				runtimeLogger().WarnContext(ctx, "cluster monitoring reconcile failed", "cluster_id", cluster.ID.String(), "error", err)
			}
		})

		return nil
	})
}

func reconcileMonitoringBackend(ctx context.Context, backend sqlc.MonitoringBackend) (sqlc.MonitoringBackend, *imonitoring.Client, bool, error) {
	// RMW site (migration 146). This tick stamps sharedThanos.status — a
	// NON-secret key — and then writes the document back. It therefore has to
	// start from the RESOLVED document, not from the stored JSONB projection:
	// re-marshalling the projection would persist a document with the
	// credential missing, and the very next tick would find a backend it can
	// no longer authenticate to. A health stamp must not be able to delete a
	// credential.
	//
	// BLAST RADIUS of a resolve failure: the WRITE, and nothing else.
	//
	// Refusing the write is mandatory — stamping the status from a document we
	// could not read persists "this backend has no credential" and converts a
	// recoverable key-management problem (wrong ASTRONOMER_ENCRYPTION_KEY, a
	// rotation that dropped a key too early) into permanent loss, on a
	// 30-second timer, with nobody watching.
	//
	// Failing the whole TICK is over-reach, and that is what an error return
	// here would do: HandleMonitoringReconcile propagates it before
	// listAllClustersPaged, so every per-cluster monitoring reconcile — which
	// needs no monitoring credential at all — stops converging for as long as
	// the key is wrong. One unreadable credential on one row would freeze the
	// whole fleet's monitoring config.
	//
	// So we log, decline the write, and carry on. That matches this function's
	// own precedent for a backend it cannot use (a failed health check logs and
	// marks degraded rather than erroring) and imonitoring.NewClient's explicit
	// decision not to hard-fail on the same condition. It also avoids asynq
	// retrying a full-fleet fan-out on a condition no retry can fix.
	//
	// The cost is that sharedThanos.status goes STALE rather than being stamped
	// "degraded" — we cannot write it without re-sealing a document we could
	// not read. The per-cluster configs still converge to "degraded" below,
	// which is where an operator looks, and the error log line names the key.
	full, err := imonitoring.ResolveAuthConfig(backend.AuthConfigEncrypted, backend.AuthConfig, monitoringDecryptor())
	credentialUnavailable := err != nil
	if credentialUnavailable {
		runtimeLogger().ErrorContext(ctx, "monitoring backend credential could not be decrypted: skipping the status write to avoid persisting a document with the credential missing",
			"backend_id", backend.ID.String(), "error", err)
	}
	authCfg := decodeJSONMapLocal(full)
	shared := mapFromAny(authCfg["sharedThanos"])
	status := "not_configured"
	var client *imonitoring.Client

	if backend.QueryUrl != "" {
		c, err := imonitoring.NewClient(imonitoring.BackendConfig{
			QueryURL:            backend.QueryUrl,
			TenantID:            backend.TenantID,
			AuthType:            backend.AuthType,
			AuthConfig:          backend.AuthConfig,
			AuthConfigEncrypted: backend.AuthConfigEncrypted,
			Decryptor:           monitoringDecryptor(),
			Logger:              runtimeLogger(),
			DefaultStepSeconds:  backend.DefaultStepSeconds,
			TimeoutSeconds:      backend.TimeoutSeconds,
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
	if credentialUnavailable {
		// The probe may well have succeeded — NewClient degrades to an
		// unauthenticated request — but a backend we cannot authenticate to is
		// not healthy, and reporting it as such would send every cluster query
		// below into an upstream 401 while claiming the backend was fine.
		status = "degraded"
	}

	// `!credentialUnavailable` is the explicit half of the guard. An
	// unresolved document decodes to an empty map, so `len(shared) > 0` would
	// happen to skip the write today — but that is a coincidence of this
	// document's shape, not a rule, and the rule is the one that matters.
	if !credentialUnavailable && len(shared) > 0 {
		shared["status"] = status
		shared["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
		authCfg["sharedThanos"] = shared
		params := sqlc.UpsertDefaultMonitoringBackendParams{
			BackendType:        backend.BackendType,
			QueryUrl:           backend.QueryUrl,
			AlertmanagerUrl:    backend.AlertmanagerUrl,
			TenantID:           backend.TenantID,
			AuthType:           backend.AuthType,
			DefaultStepSeconds: backend.DefaultStepSeconds,
			TimeoutSeconds:     backend.TimeoutSeconds,
			CreatedByID:        backend.CreatedByID,
		}
		if err := imonitoring.SealInto(&params, authCfg, monitoringEncryptor()); err != nil {
			return backend, client, status == "healthy", err
		}
		updated, err := runtimeDeps.Queries.UpsertDefaultMonitoringBackend(ctx, params)
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
