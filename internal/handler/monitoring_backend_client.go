package handler

import (
	"context"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h *MonitoringHandler) backendClient(ctx context.Context, clusterID string) (*imonitoring.Client, monitoringContext, bool, error) {
	if h.queries == nil {
		return nil, monitoringContext{}, false, nil
	}
	clusterUUID, err := uuid.Parse(clusterID)
	if err != nil {
		return nil, monitoringContext{}, false, err
	}
	if joined, err := h.queries.GetClusterMonitoringContext(ctx, clusterUUID); err == nil {
		if !joined.ThanosSidecarEnabled && joined.LastAppliedSpecHash != "" && joined.Status != "uninstalled" {
			client, err := imonitoring.NewClusterClient(ctx, h.requester, clusterID, joined.StackNamespace, joined.PrometheusReleaseName)
			return client, monitoringContext{Local: true, DefaultStep: joined.DefaultStepSeconds}, true, err
		}
		client, err := imonitoring.NewClient(imonitoring.BackendConfig{
			QueryURL:            joined.QueryUrl,
			TenantID:            joined.TenantID,
			AuthType:            joined.AuthType,
			AuthConfig:          joined.AuthConfig,
			AuthConfigEncrypted: joined.AuthConfigEncrypted,
			Decryptor:           h.monitoringDecryptor(),
			Logger:              h.log,
			DefaultStepSeconds:  joined.DefaultStepSeconds,
			TimeoutSeconds:      joined.TimeoutSeconds,
		})
		if err != nil {
			return nil, monitoringContext{}, false, err
		}
		return client, monitoringContext{
			ClusterLabel:      joined.ClusterLabel,
			ClusterLabelValue: defaultString(joined.ClusterLabelValue, clusterID),
			DefaultStep:       joined.DefaultStepSeconds,
		}, true, nil
	} else if err != pgx.ErrNoRows {
		return nil, monitoringContext{}, false, err
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, monitoringContext{}, false, nil
		}
		return nil, monitoringContext{}, false, err
	}
	client, err := imonitoring.NewClient(imonitoring.BackendConfig{
		QueryURL:            backend.QueryUrl,
		TenantID:            backend.TenantID,
		AuthType:            backend.AuthType,
		AuthConfig:          backend.AuthConfig,
		AuthConfigEncrypted: backend.AuthConfigEncrypted,
		Decryptor:           h.monitoringDecryptor(),
		Logger:              h.log,
		DefaultStepSeconds:  backend.DefaultStepSeconds,
		TimeoutSeconds:      backend.TimeoutSeconds,
	})
	if err != nil {
		return nil, monitoringContext{}, false, err
	}
	return client, monitoringContext{ClusterLabel: "cluster_id", ClusterLabelValue: clusterID, DefaultStep: backend.DefaultStepSeconds}, true, nil
}
