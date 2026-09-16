package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
)

func (h *MonitoringHandler) sharedThanosPayload(ctx context.Context, r *http.Request) (SharedThanosStackRequest, map[string]any, objectStoreSecretSpec, sqlc.MonitoringBackend, error) {
	if h.queries == nil {
		return SharedThanosStackRequest{}, nil, objectStoreSecretSpec{}, sqlc.MonitoringBackend{}, fmt.Errorf("monitoring store not configured")
	}
	if h.helm == nil {
		return SharedThanosStackRequest{}, nil, objectStoreSecretSpec{}, sqlc.MonitoringBackend{}, fmt.Errorf("helm requester not configured")
	}

	var req SharedThanosStackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		return SharedThanosStackRequest{}, nil, objectStoreSecretSpec{}, sqlc.MonitoringBackend{}, fmt.Errorf("invalid JSON body")
	}
	if req.ManagementClusterID == "" {
		req.ManagementClusterID = r.URL.Query().Get("clusterId")
	}
	if req.ManagementClusterID == "" {
		return SharedThanosStackRequest{}, nil, objectStoreSecretSpec{}, sqlc.MonitoringBackend{}, fmt.Errorf("managementClusterId is required")
	}
	if req.Namespace == "" {
		req.Namespace = "monitoring"
	}
	if req.ReleaseName == "" {
		req.ReleaseName = "thanos"
	}
	if req.ChartVersion == "" {
		req.ChartVersion = "1.23.0"
	}
	if req.QueryReplicas <= 0 {
		req.QueryReplicas = 2
	}
	if req.StoreGatewayReplicas <= 0 {
		req.StoreGatewayReplicas = 1
	}
	if req.CompactorReplicas <= 0 {
		req.CompactorReplicas = 1
	}
	if req.StorageConfigID == "" {
		return SharedThanosStackRequest{}, nil, objectStoreSecretSpec{}, sqlc.MonitoringBackend{}, fmt.Errorf("storageConfigId is required")
	}

	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return SharedThanosStackRequest{}, nil, objectStoreSecretSpec{}, sqlc.MonitoringBackend{}, fmt.Errorf("default monitoring backend is not configured")
	}
	// nil authorizer, deliberately: sharedStackLifecycle's preamble has already
	// required a FLEET-WIDE monitoring grant (authorizeGlobalAction against
	// uuid.Nil) to reach this payload builder, which is a strictly higher bar
	// than clusterStorageConfigAuthorizer's most permissive clause, and the
	// secret lands on the management cluster rather than a tenant's.
	secretSpec, err := h.objectStoreSecretSpec(ctx, req.StorageConfigID, req.ObjectStorageSecretName, req.ReleaseName+"-objstore", nil)
	if err != nil {
		return SharedThanosStackRequest{}, nil, objectStoreSecretSpec{}, sqlc.MonitoringBackend{}, err
	}
	req.ObjectStorageSecretName = secretSpec.Name

	values := map[string]any{
		"objstoreConfig": map[string]any{
			"create": false,
			"name":   secretSpec.Name,
			"key":    secretSpec.Key,
		},
		"query": map[string]any{
			"enabled":            true,
			"replicas":           req.QueryReplicas,
			"enableDnsDiscovery": false,
		},
		"queryFrontend": map[string]any{
			"enabled": true,
		},
		"bucketWeb": map[string]any{
			"enabled": true,
		},
		"compact": map[string]any{
			"enabled": true,
			"persistence": map[string]any{
				"enabled": true,
				"size":    "20Gi",
			},
		},
		"storeGateway": map[string]any{
			"enabled":  true,
			"replicas": req.StoreGatewayReplicas,
			"persistence": map[string]any{
				"enabled": true,
				"size":    "20Gi",
			},
		},
		"rule": map[string]any{
			"enabled":  true,
			"replicas": 1,
			"rules": map[string]any{
				"create": false,
				"name":   "astronomer-ruler-rules",
			},
		},
		"receive": map[string]any{
			"enabled": false,
		},
		"metrics": map[string]any{
			"enabled": true,
		},
	}
	if backend.AlertmanagerUrl != "" {
		values["rule"].(map[string]any)["alertmanagersConfig"] = map[string]any{
			"create": false,
			"name":   "astronomer-thanos-rule-alertmanagers",
			"key":    "config",
		}
	}
	return req, values, secretSpec, backend, nil
}

// sharedStackMetadata reads one family's deployment metadata out of the shared
// monitoring_backends.auth_config bag. A missing or malformed entry is an empty
// map, never nil-panicking downstream.
func sharedStackMetadata(backend sqlc.MonitoringBackend, key string) map[string]any {
	authCfg := decodeJSONMap(backend.AuthConfig)
	raw, ok := authCfg[key]
	if !ok {
		return map[string]any{}
	}
	metadata, ok := raw.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return metadata
}

// sharedThanosMetadata is sharedStackMetadata bound to the Thanos family, kept
// for the one caller outside this file (alerting.go, resolving the ruler's
// target cluster). The lifecycle driver reads the key off its own config.
func sharedThanosMetadata(backend sqlc.MonitoringBackend) map[string]any {
	return sharedStackMetadata(backend, "sharedThanos")
}

func (h *MonitoringHandler) updateSharedThanosMetadata(ctx context.Context, backend sqlc.MonitoringBackend, req SharedThanosStackRequest, status string) error {
	if h.queries == nil {
		return nil
	}
	return h.updateSharedThanosMetadataWith(ctx, h.queries, backend, req, status)
}

func (h *MonitoringHandler) updateSharedThanosMetadataWith(ctx context.Context, q monitoringSharedMutationWriter, backend sqlc.MonitoringBackend, req SharedThanosStackRequest, status string) error {
	appliedSpecHash := specHash(map[string]any{
		"managementClusterId":     req.ManagementClusterID,
		"namespace":               defaultString(req.Namespace, "monitoring"),
		"releaseName":             defaultString(req.ReleaseName, "thanos"),
		"storageConfigId":         req.StorageConfigID,
		"objectStorageSecretName": req.ObjectStorageSecretName,
		"chartVersion":            req.ChartVersion,
		"queryReplicas":           req.QueryReplicas,
		"storeGatewayReplicas":    req.StoreGatewayReplicas,
		"compactorReplicas":       req.CompactorReplicas,
		"autoRollbackOnFailure":   boolPtrValue(req.AutoRollbackOnFailure),
	})
	// RMW site (migration 146): this mutates a NON-secret key
	// (sharedThanos deployment metadata) and must not lose the credential
	// stored alongside it. Resolve first; a failure aborts the write.
	authCfg, err := resolveMonitoringBackendAuthConfig(backend, h.monitoringDecryptor())
	if err != nil {
		return fmt.Errorf("resolve monitoring backend auth_config: %w", err)
	}
	authCfg["sharedThanos"] = map[string]any{
		"managementClusterId":     req.ManagementClusterID,
		"namespace":               defaultString(req.Namespace, "monitoring"),
		"releaseName":             defaultString(req.ReleaseName, "thanos"),
		"storageConfigId":         req.StorageConfigID,
		"objectStorageSecretName": req.ObjectStorageSecretName,
		"status":                  status,
		"chartVersion":            req.ChartVersion,
		"queryReplicas":           req.QueryReplicas,
		"storeGatewayReplicas":    req.StoreGatewayReplicas,
		"compactorReplicas":       req.CompactorReplicas,
		"lastAppliedSpecHash":     appliedSpecHash,
		"managedAssetHashes": map[string]any{
			"objstoreSecret": specHash(map[string]any{
				"name": req.ObjectStorageSecretName,
				"id":   req.StorageConfigID,
			}),
		},
		"updatedAt": time.Now().UTC().Format(time.RFC3339),
	}
	params := sqlc.UpsertDefaultMonitoringBackendParams{
		BackendType:        backend.BackendType,
		QueryUrl:           defaultSharedThanosQueryURL(backend.QueryUrl, req),
		AlertmanagerUrl:    backend.AlertmanagerUrl,
		TenantID:           backend.TenantID,
		AuthType:           backend.AuthType,
		DefaultStepSeconds: backend.DefaultStepSeconds,
		TimeoutSeconds:     backend.TimeoutSeconds,
		CreatedByID:        backend.CreatedByID,
	}
	if err := imonitoring.SealInto(&params, authCfg, h.monitoringSealer()); err != nil {
		return err
	}
	_, err = q.UpsertDefaultMonitoringBackend(ctx, params)
	return err
}
