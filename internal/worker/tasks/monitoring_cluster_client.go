package tasks

import (
	"context"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func monitoringClientForCluster(ctx context.Context, clusterID uuid.UUID) (*imonitoring.Client, monitoringSelector, bool, error) {
	if runtimeDependencies(ctx).Queries == nil {
		return nil, monitoringSelector{}, false, nil
	}
	if joined, err := runtimeDependencies(ctx).Queries.GetClusterMonitoringContext(ctx, clusterID); err == nil {
		if !joined.ThanosSidecarEnabled && joined.LastAppliedSpecHash != "" && joined.Status != "uninstalled" {
			client, err := imonitoring.NewClusterClient(ctx, runtimeDependencies(ctx).K8s, clusterID.String(), joined.StackNamespace, joined.PrometheusReleaseName)
			return client, monitoringSelector{Local: true}, true, err
		}
		client, err := imonitoring.NewClient(imonitoring.BackendConfig{
			QueryURL:            joined.QueryUrl,
			TenantID:            joined.TenantID,
			AuthType:            joined.AuthType,
			AuthConfig:          joined.AuthConfig,
			AuthConfigEncrypted: joined.AuthConfigEncrypted,
			Decryptor:           monitoringDecryptor(ctx),
			Logger:              runtimeLogger(ctx),
			DefaultStepSeconds:  joined.DefaultStepSeconds,
			TimeoutSeconds:      joined.TimeoutSeconds,
		})
		if err != nil {
			return nil, monitoringSelector{}, false, err
		}
		return client, monitoringSelector{
			Label: joined.ClusterLabel,
			Value: joined.ClusterLabelValue,
		}, true, nil
	} else if err != pgx.ErrNoRows {
		return nil, monitoringSelector{}, false, err
	}
	backend, err := runtimeDependencies(ctx).Queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, monitoringSelector{}, false, nil
		}
		return nil, monitoringSelector{}, false, err
	}
	client, err := imonitoring.NewClient(imonitoring.BackendConfig{
		QueryURL:            backend.QueryUrl,
		TenantID:            backend.TenantID,
		AuthType:            backend.AuthType,
		AuthConfig:          backend.AuthConfig,
		AuthConfigEncrypted: backend.AuthConfigEncrypted,
		Decryptor:           monitoringDecryptor(ctx),
		Logger:              runtimeLogger(ctx),
		DefaultStepSeconds:  backend.DefaultStepSeconds,
		TimeoutSeconds:      backend.TimeoutSeconds,
	})
	if err != nil {
		return nil, monitoringSelector{}, false, err
	}
	return client, monitoringSelector{Label: "cluster_id", Value: clusterID.String()}, true, nil
}
