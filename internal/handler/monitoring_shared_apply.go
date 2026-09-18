package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func (h *MonitoringHandler) applySharedThanosStack(ctx context.Context, msgType protocol.MessageType, req SharedThanosStackRequest, secretSpec objectStoreSecretSpec, values map[string]any) (*protocol.HelmResultPayload, error) {
	if h.helm == nil {
		return nil, fmt.Errorf("helm requester not configured")
	}
	if err := h.ensureObjectStoreSecret(ctx, req.ManagementClusterID, req.Namespace, secretSpec); err != nil {
		return nil, err
	}
	return h.helm.Do(ctx, req.ManagementClusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: req.ReleaseName,
		Namespace:   req.Namespace,
		ChartName:   "thanos",
		RepoURL:     "https://stevehipwell.github.io/helm-charts/",
		Version:     req.ChartVersion,
		Values:      values,
		Timeout:     1200,
	})
}

func (h *MonitoringHandler) applySharedAlertmanager(ctx context.Context, msgType protocol.MessageType, req SharedAlertmanagerRequest, values map[string]any) (*protocol.HelmResultPayload, error) {
	if h.helm == nil {
		return nil, fmt.Errorf("helm requester not configured")
	}
	return h.helm.Do(ctx, req.ManagementClusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: req.ReleaseName,
		Namespace:   req.Namespace,
		ChartName:   "alertmanager",
		RepoURL:     "https://prometheus-community.github.io/helm-charts",
		Version:     req.ChartVersion,
		Values:      values,
		Timeout:     1200,
	})
}

func (h *MonitoringHandler) applySharedLokiStack(ctx context.Context, msgType protocol.MessageType, req SharedLokiRequest, values map[string]any) (*protocol.HelmResultPayload, error) {
	if h.helm == nil {
		return nil, fmt.Errorf("helm requester not configured")
	}
	if values == nil {
		values = map[string]any{}
	}
	values["extraObjects"] = lokiFamilyExtraObjects(req, h.proxyImage, h.grafanaExpose)
	return h.helm.Do(ctx, req.ManagementClusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: req.ReleaseName,
		Namespace:   req.Namespace,
		ChartName:   sharedLokiChartName,
		RepoURL:     sharedLokiChartRepo,
		Version:     req.ChartVersion,
		Values:      values,
		Timeout:     1200,
	})
}

func (h *MonitoringHandler) applySharedGrafanaStack(ctx context.Context, msgType protocol.MessageType, req SharedGrafanaRequest, values map[string]any) (*protocol.HelmResultPayload, error) {
	if h.helm == nil {
		return nil, fmt.Errorf("helm requester not configured")
	}
	// Re-render sidecar ConfigMaps from live backend metadata so a Thanos
	// family that became healthy after enqueue is included without a second
	// form submit.
	if values == nil {
		values = map[string]any{}
	}
	if h.queries != nil {
		if backend, err := h.queries.GetDefaultMonitoringBackend(ctx); err == nil {
			values["extraObjects"] = grafanaFamilyExtraObjects(req, backend, h.proxyImage, h.serverURL, h.grafanaExpose)
		}
	}
	values["extraConfigmapMounts"] = []any{grafanaClusterFolderProvidersMount()}
	return h.helm.Do(ctx, req.ManagementClusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: req.ReleaseName,
		Namespace:   req.Namespace,
		ChartName:   "grafana",
		RepoURL:     "https://grafana.github.io/helm-charts",
		Version:     req.ChartVersion,
		Values:      values,
		Timeout:     1200,
	})
}

func sanitizeMonitoringValues(values map[string]any) map[string]any {
	raw, err := json.Marshal(values)
	if err != nil {
		return map[string]any{}
	}
	var cloned map[string]any
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return map[string]any{}
	}
	redactSensitiveMap(cloned)
	return cloned
}

func redactSensitiveMap(data map[string]any) {
	for key, value := range data {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "access_key") || strings.Contains(lower, "accesskey") || strings.Contains(lower, "secret_key") || strings.Contains(lower, "objstoreconfig") {
			data[key] = "***redacted***"
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			redactSensitiveMap(typed)
		case []any:
			for _, item := range typed {
				if m, ok := item.(map[string]any); ok {
					redactSensitiveMap(m)
				}
			}
		}
	}
}

// storageConfigAuthorizer approves — or refuses — a caller's reference to one
// backup_storage_configs row. objectStoreSecretSpec calls it after loading the
// row and BEFORE reading anything out of it.
//
// It exists because storageConfigId arrives in the REQUEST BODY and names a
// global object: GetBackupStorageConfigByID takes an id and nothing else, and
// the row it returns carries live S3 access/secret keys that this file writes
// verbatim into a Secret in a namespace of the caller's cluster. The backups
// API gates the same rows on rbac.ResourceBackups and never surfaces their raw
// keys at all (backups.go storageResponse). While the per-cluster monitoring
// routes were evaluated as a GLOBAL check, "the caller reached this code" was
// itself proof of a fleet-wide grant; now that a single-cluster tenant can
// reach them, the reference needs its own authorization.
