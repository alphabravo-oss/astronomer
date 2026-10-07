package handler

import (
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

func clusterMonitoringReplaceRequired(cfg sqlc.ClusterMonitoringConfig, exists bool, req MonitoringStackRequest) (bool, []string) {
	if !exists || cfg.Status == "uninstalled" {
		return false, nil
	}
	reasons := []string{}
	if cfg.StackNamespace != "" && cfg.StackNamespace != req.Namespace {
		reasons = append(reasons, "namespace change")
	}
	if cfg.PrometheusReleaseName != "" && cfg.PrometheusReleaseName != req.ReleaseName {
		reasons = append(reasons, "release name change")
	}
	if cfg.StorageConfigID.Valid != (req.StorageConfigID != "") {
		reasons = append(reasons, "object storage mode change")
	} else if cfg.StorageConfigID.Valid && req.StorageConfigID != "" && uuid.UUID(cfg.StorageConfigID.Bytes).String() != req.StorageConfigID {
		reasons = append(reasons, "object storage configuration change")
	}
	if cfg.ObjectStorageSecretName != "" && req.ObjectStorageSecretName != "" && cfg.ObjectStorageSecretName != req.ObjectStorageSecretName {
		reasons = append(reasons, "object storage secret change")
	}
	if cfg.StorageClass != req.StorageClass {
		reasons = append(reasons, "storage class change")
	}
	if cfg.StorageSize != "" && cfg.StorageSize != req.StorageSize {
		reasons = append(reasons, "storage size change")
	}
	return len(reasons) > 0, reasons
}
