package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
)

const (
	sharedLokiChartRepo       = "https://grafana.github.io/helm-charts"
	sharedLokiChartName       = "loki"
	sharedLokiDefaultRelease  = "astronomer-loki"
	sharedLokiDefaultChart    = "6.27.0"
	sharedLokiSchemaFrom      = "2024-01-01"
	lokiAuthListenPort        = 8080
	lokiWALProbeName          = "astronomer-loki-wal-probe"
	lokiWALProbeTimeout       = 15 * time.Second
	lokiTokenHashSecretName   = "astronomer-loki-token-hashes"
	lokiQueryACLConfigMapName = "astronomer-loki-query-acl"
	lokiIngestTLSSecretName   = "astronomer-loki-ingest-tls"
)

func (h *MonitoringHandler) sharedLokiPayload(ctx context.Context, r *http.Request) (SharedLokiRequest, map[string]any, sqlc.MonitoringBackend, error) {
	if h.queries == nil {
		return SharedLokiRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("monitoring store not configured")
	}
	if h.helm == nil {
		return SharedLokiRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("helm requester not configured")
	}

	var req SharedLokiRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		return SharedLokiRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("invalid JSON body")
	}
	if req.ManagementClusterID == "" {
		req.ManagementClusterID = r.URL.Query().Get("clusterId")
	}
	if req.ManagementClusterID == "" {
		return SharedLokiRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("managementClusterId is required")
	}
	if req.Namespace == "" {
		req.Namespace = "monitoring"
	}
	if req.ReleaseName == "" {
		req.ReleaseName = sharedLokiDefaultRelease
	}
	if req.ChartVersion == "" {
		req.ChartVersion = sharedLokiDefaultChart
	}
	if req.Retention == "" {
		req.Retention = "14d"
	}
	if req.StorageClass == "" {
		req.StorageClass = sizerDefaultStorageClass
	}
	if req.StorageConfigID == "" {
		return SharedLokiRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("storageConfigId is required")
	}
	if strings.ContainsAny(req.IngestHostname, "\n\r\t /") {
		return SharedLokiRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("ingestHostname must be a hostname")
	}
	if req.IngestHostname == "" {
		return SharedLokiRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("ingestHostname is required and is never derived from the Astronomer ingress host")
	}
	if req.Mode != "" && req.Mode != sizerModeSingleBinary && req.Mode != sizerModeSimpleScalable {
		return SharedLokiRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("mode must be empty, %q, or %q", sizerModeSingleBinary, sizerModeSimpleScalable)
	}
	if h == nil || strings.TrimSpace(h.proxyImage) == "" {
		return SharedLokiRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("loki-auth image is not configured (ASTRONOMER_SERVER_IMAGE)")
	}

	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return SharedLokiRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("default monitoring backend is not configured")
	}

	s3, prefix, err := h.lokiS3FromStorageConfig(ctx, req.StorageConfigID)
	if err != nil {
		return SharedLokiRequest{}, nil, sqlc.MonitoringBackend{}, err
	}
	existing := sharedStackMetadata(backend, "sharedLoki")
	if req.Mode == "" {
		req.Mode = stringFromMap(existing, "mode")
	}
	if req.WalStorageSize == "" {
		req.WalStorageSize = stringFromMap(existing, "walStorageSize")
	}
	if req.WalStorageSize == "" {
		req.WalStorageSize = lokiDefaultWALSize(req.Mode)
	}
	if req.ObjectStorageSecretName == "" {
		req.ObjectStorageSecretName = stringFromMap(existing, "objectStorageSecretName")
	}

	values := h.sharedLokiHelmValues(req, existing, s3, prefix)
	return req, values, backend, nil
}

func lokiDefaultWALSize(mode string) string {
	if mode == sizerModeSimpleScalable {
		return "10Gi"
	}
	return "10Gi"
}

func (h *MonitoringHandler) lokiS3FromStorageConfig(ctx context.Context, storageConfigID string) (map[string]any, string, error) {
	id, err := uuid.Parse(storageConfigID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid storageConfigId")
	}
	cfg, err := h.queries.GetBackupStorageConfigByID(ctx, id)
	if err != nil {
		return nil, "", fmt.Errorf("backup storage config not found")
	}
	if cfg.Bucket == "" {
		return nil, "", fmt.Errorf("storage config bucket is required")
	}
	accessKey, secretKey, err := h.storageCredentials(cfg)
	if err != nil {
		return nil, "", err
	}
	prefix := computedLokiPrefix(cfg.Prefix)
	endpoint := strings.TrimSpace(cfg.EndpointUrl)
	insecure := strings.HasPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}
	region := defaultString(cfg.Region, "us-east-1")
	return map[string]any{
		"type":                "s3",
		"use_thanos_objstore": true,
		"bucketNames": map[string]any{
			"chunks": cfg.Bucket,
			"ruler":  cfg.Bucket,
			"admin":  cfg.Bucket,
		},
		"object_store": map[string]any{
			"type":   "s3",
			"prefix": prefix,
			"s3": map[string]any{
				"endpoint":          endpoint,
				"region":            region,
				"access_key_id":     accessKey,
				"secret_access_key": secretKey,
				"insecure":          insecure,
			},
		},
	}, prefix, nil
}

func (h *MonitoringHandler) sharedLokiPrecheck(ctx context.Context, req SharedLokiRequest, op string) (int, string, string, bool) {
	if op != "install" && op != "replace" {
		return 0, "", "", true
	}
	snap := h.collectSizerSnapshotFor(ctx, req.ManagementClusterID, req.StorageClass, req.StorageConfigID)
	eval := evaluateSizer(snap.input)
	h.stampSharedLokiSizerVerdict(ctx, eval.Verdicts.Loki)

	selected := ""
	if eval.Verdicts.Loki.Mode != nil {
		selected = *eval.Verdicts.Loki.Mode
	}
	requested := req.Mode
	if requested != "" && selected != "" && requested != selected {
		if !(requested == sizerModeSingleBinary && selected == sizerModeSimpleScalable) {
			msg := "requested mode " + requested + " is not offered (sizer selected " + selected + ")"
			return http.StatusPreconditionFailed, apierror.SizerFailed, msg, false
		}
		selected = requested
	}
	if requested != "" && selected == "" {
		selected = requested
	}
	if eval.Verdicts.Loki.Result != "pass" {
		msg := strings.Join(eval.Verdicts.Loki.Reasons, ", ")
		if msg == "" {
			msg = "loki sizer verdict failed"
		}
		return http.StatusPreconditionFailed, apierror.SizerFailed, msg, false
	}
	if selected == "" {
		selected = sizerModeSingleBinary
	}

	skip := req.SkipDiskCheck != nil && *req.SkipDiskCheck
	if req.SkipDiskCheck == nil {
		if backend, err := h.queries.GetDefaultMonitoringBackend(ctx); err == nil {
			skip = boolFromAny(sharedStackMetadata(backend, "sharedLoki")["skipDiskCheck"])
		}
	}
	status, code, msg, ok := h.probeAndCacheLokiWAL(ctx, req, selected, skip)
	if !ok {
		return status, code, msg, false
	}
	return 0, "", "", true
}

func (h *MonitoringHandler) probeAndCacheLokiWAL(ctx context.Context, req SharedLokiRequest, mode string, skipDiskCheck bool) (int, string, string, bool) {
	need := int64(sizerWALSingleBinaryBytes)
	size := "10Gi"
	if mode == sizerModeSimpleScalable {
		need = sizerWALSimpleScalableBytes
		size = "20Gi"
	}
	known, bytes, reason := h.probeLokiWALCapacity(ctx, req.ManagementClusterID, defaultString(req.Namespace, "monitoring"), req.StorageClass, size)
	if known {
		_ = h.cacheSharedLokiWAL(ctx, true, bytes, time.Now().UTC())
		if bytes < need {
			return http.StatusPreconditionFailed, apierror.SizerFailed, "wal_too_small", false
		}
		return 0, "", "", true
	}
	if skipDiskCheck {
		_ = h.cacheSharedLokiWAL(ctx, false, 0, time.Now().UTC())
		return 0, "", "", true
	}
	if reason == "" {
		reason = "wal_capacity_unknown"
	}
	return http.StatusPreconditionFailed, apierror.SizerFailed, reason, false
}

func (h *MonitoringHandler) probeLokiWALCapacity(ctx context.Context, clusterID, namespace, storageClass, size string) (known bool, bytes int64, failReason string) {
	if h == nil || h.requester == nil || clusterID == "" {
		return false, 0, "wal_capacity_unknown"
	}
	ns := defaultString(namespace, "monitoring")
	sc := defaultString(storageClass, sizerDefaultStorageClass)
	needBytes := walSizeBytes(size)
	name := lokiWALProbeName
	path := fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims/%s", ns, name)
	_, _ = h.requester.Do(ctx, clusterID, http.MethodDelete, path, nil, requestHeaders(""))

	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "astronomer-go",
				"astronomer.io/component":      "loki-wal-probe",
			},
		},
		"spec": map[string]any{
			"accessModes":      []any{"ReadWriteOnce"},
			"storageClassName": sc,
			"resources": map[string]any{
				"requests": map[string]any{"storage": size},
			},
		},
	})
	if err != nil {
		return false, 0, "wal_capacity_unknown"
	}
	createPath := fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims", ns)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodPost, createPath, body, requestHeaders("application/json"))
	defer func() {
		_, _ = h.requester.Do(ctx, clusterID, http.MethodDelete, path, nil, requestHeaders(""))
	}()
	if err != nil || resp == nil || resp.StatusCode >= http.StatusBadRequest {
		return false, 0, "wal_too_small"
	}

	deadline := time.Now().Add(lokiWALProbeTimeout)
	for {
		got, gerr := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
		if gerr == nil && got != nil && got.StatusCode < http.StatusBadRequest {
			var pvc map[string]any
			if parseJSONResponse(got, &pvc) == nil {
				status, _ := pvc["status"].(map[string]any)
				phase, _ := status["phase"].(string)
				switch strings.ToLower(phase) {
				case "bound":
					return true, needBytes, ""
				case "lost":
					return false, 0, "wal_too_small"
				}
			}
		}
		if time.Now().After(deadline) {
			return false, 0, "wal_capacity_unknown"
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func walSizeBytes(size string) int64 {
	s := strings.TrimSpace(strings.ToLower(size))
	switch s {
	case "20gi":
		return sizerWALSimpleScalableBytes
	case "10gi":
		return sizerWALSingleBinaryBytes
	}
	return sizerWALSingleBinaryBytes
}

func (h *MonitoringHandler) cacheSharedLokiWAL(ctx context.Context, known bool, bytes int64, checkedAt time.Time) error {
	if h == nil || h.queries == nil {
		return nil
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return err
	}
	authCfg, err := resolveMonitoringBackendAuthConfig(backend, h.monitoringDecryptor())
	if err != nil {
		return err
	}
	meta := mapFromMapValue(authCfg["sharedLoki"])
	if meta == nil {
		meta = map[string]any{}
	}
	meta["walCapacityKnown"] = known
	meta["walCapacityBytes"] = bytes
	meta["walCheckedAt"] = checkedAt.Format(time.RFC3339)
	authCfg["sharedLoki"] = meta
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
	if err := imonitoring.SealInto(&params, authCfg, h.monitoringSealer()); err != nil {
		return err
	}
	_, err = h.queries.UpsertDefaultMonitoringBackend(ctx, params)
	return err
}

func (h *MonitoringHandler) stampSharedLokiSizerVerdict(ctx context.Context, verdict sizerLokiVerdict) {
	if h == nil || h.queries == nil {
		return
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return
	}
	authCfg, err := resolveMonitoringBackendAuthConfig(backend, h.monitoringDecryptor())
	if err != nil {
		return
	}
	meta := mapFromMapValue(authCfg["sharedLoki"])
	if meta == nil {
		meta = map[string]any{}
	}
	entry := map[string]any{
		"result":   verdict.Result,
		"reasons":  verdict.Reasons,
		"warnings": verdict.Warnings,
	}
	if verdict.Mode != nil {
		entry["mode"] = *verdict.Mode
	}
	meta["lastSizerVerdict"] = entry
	authCfg["sharedLoki"] = meta
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
	if err := imonitoring.SealInto(&params, authCfg, h.monitoringSealer()); err != nil {
		return
	}
	_, _ = h.queries.UpsertDefaultMonitoringBackend(ctx, params)
}

func (h *MonitoringHandler) updateSharedLokiMetadata(ctx context.Context, backend sqlc.MonitoringBackend, req SharedLokiRequest, status string) error {
	if h.queries == nil {
		return nil
	}
	// Precheck caches WAL/lastSizerVerdict on a later row than payload's
	// snapshot. Re-read so persist cannot clobber those keys.
	if live, err := h.queries.GetDefaultMonitoringBackend(ctx); err == nil {
		backend = live
	}
	resolvedRollback := h.resolveAutoRollbackPolicy(backend, req.AutoRollbackOnFailure)
	ns := defaultString(req.Namespace, "monitoring")
	release := defaultString(req.ReleaseName, sharedLokiDefaultRelease)
	prefix := ""
	if req.StorageConfigID != "" {
		if _, p, err := h.lokiS3FromStorageConfig(ctx, req.StorageConfigID); err == nil {
			prefix = p
		}
	}
	appliedSpecHash := specHash(map[string]any{
		"managementClusterId":     req.ManagementClusterID,
		"namespace":               ns,
		"releaseName":             release,
		"chartVersion":            req.ChartVersion,
		"storageConfigId":         req.StorageConfigID,
		"objectStorageSecretName": req.ObjectStorageSecretName,
		"ingestHostname":          req.IngestHostname,
		"storageClass":            req.StorageClass,
		"walStorageSize":          req.WalStorageSize,
		"mode":                    req.Mode,
		"retention":               req.Retention,
		"skipDiskCheck":           boolPtrValue(req.SkipDiskCheck),
		"autoRollbackOnFailure":   resolvedRollback,
	})
	authCfg, err := resolveMonitoringBackendAuthConfig(backend, h.monitoringDecryptor())
	if err != nil {
		return fmt.Errorf("resolve monitoring backend auth_config: %w", err)
	}
	existing := mapFromMapValue(authCfg["sharedLoki"])
	mode := req.Mode
	if mode == "" {
		mode = stringFromMap(existing, "mode")
	}
	skip := boolFromAny(existing["skipDiskCheck"])
	if req.SkipDiskCheck != nil {
		skip = *req.SkipDiskCheck
	}
	if prefix == "" {
		prefix = stringFromMap(existing, "computedLokiPrefix")
	}
	schemaFrom := defaultString(stringFromMap(existing, "schemaFrom"), sharedLokiSchemaFrom)
	queryURL, authURL := lokiDerivedURLs(release, ns)
	authCfg["sharedLoki"] = map[string]any{
		"managementClusterId":     req.ManagementClusterID,
		"namespace":               ns,
		"releaseName":             release,
		"status":                  status,
		"chartVersion":            req.ChartVersion,
		"storageConfigId":         req.StorageConfigID,
		"objectStorageSecretName": req.ObjectStorageSecretName,
		"ingestHostname":          req.IngestHostname,
		"ingestPublic":            lokiIngestShouldBePublic(status, h.lokiHasIngestTokens(ctx), req.IngestHostname),
		"storageClass":            req.StorageClass,
		"walStorageSize":          req.WalStorageSize,
		"mode":                    mode,
		"retention":               req.Retention,
		"skipDiskCheck":           skip,
		"autoRollbackOnFailure":   resolvedRollback,
		"computedLokiPrefix":      prefix,
		"schemaFrom":              schemaFrom,
		"queryUrl":                queryURL,
		"authUrl":                 authURL,
		"walCapacityKnown":        boolFromAny(existing["walCapacityKnown"]),
		"walCapacityBytes":        int64FromAny(existing["walCapacityBytes"]),
		"walCheckedAt":            stringFromMap(existing, "walCheckedAt"),
		"lastSizerVerdict":        existing["lastSizerVerdict"],
		"lastAppliedSpecHash":     appliedSpecHash,
		"updatedAt":               time.Now().UTC().Format(time.RFC3339),
	}
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
	if err := imonitoring.SealInto(&params, authCfg, h.monitoringSealer()); err != nil {
		return err
	}
	if _, err = h.queries.UpsertDefaultMonitoringBackend(ctx, params); err != nil {
		return err
	}
	if status == "uninstalled" || status == "not_configured" {
		_ = h.ReconcileLokiIngest(ctx)
	}
	return nil
}

func lokiDerivedURLs(release, ns string) (queryURL, authURL string) {
	release = defaultString(release, sharedLokiDefaultRelease)
	ns = defaultString(ns, "monitoring")
	queryURL = fmt.Sprintf("http://%s-gateway.%s.svc.cluster.local", release, ns)
	authURL = fmt.Sprintf("http://%s-auth.%s.svc.cluster.local:%d", release, ns, lokiAuthListenPort)
	return queryURL, authURL
}

func sharedLokiReplaceRequired(metadata map[string]any, req SharedLokiRequest) (bool, []string) {
	if len(metadata) == 0 || stringFromMap(metadata, "status") == "not_configured" || stringFromMap(metadata, "status") == "uninstalled" {
		return false, nil
	}
	reasons := []string{}
	if current := stringFromMap(metadata, "namespace"); current != "" && current != req.Namespace {
		reasons = append(reasons, "namespace change")
	}
	if current := stringFromMap(metadata, "releaseName"); current != "" && current != req.ReleaseName {
		reasons = append(reasons, "release name change")
	}
	if current := stringFromMap(metadata, "storageConfigId"); current != req.StorageConfigID {
		reasons = append(reasons, "object storage configuration change")
	}
	if current := stringFromMap(metadata, "objectStorageSecretName"); current != "" && current != req.ObjectStorageSecretName {
		reasons = append(reasons, "object storage secret change")
	}
	if current := stringFromMap(metadata, "storageClass"); current != req.StorageClass {
		reasons = append(reasons, "storage class change")
	}
	if current := stringFromMap(metadata, "walStorageSize"); current != "" && req.WalStorageSize != "" && current != req.WalStorageSize {
		reasons = append(reasons, "WAL size change")
	}
	if current := stringFromMap(metadata, "mode"); current != "" && req.Mode != "" && current != req.Mode {
		reasons = append(reasons, "mode change")
	}
	return len(reasons) > 0, reasons
}

func (h *MonitoringHandler) sharedLokiHelmValues(req SharedLokiRequest, existing map[string]any, storage map[string]any, prefix string) map[string]any {
	mode := req.Mode
	if mode == "" {
		mode = stringFromMap(existing, "mode")
	}
	if mode == "" {
		mode = sizerModeSingleBinary
	}
	if existingPrefix := stringFromMap(existing, "computedLokiPrefix"); existingPrefix != "" {
		if stringFromMap(existing, "storageConfigId") == req.StorageConfigID || req.StorageConfigID == "" {
			prefix = existingPrefix
		}
	}
	if prefix == "" {
		prefix = "loki"
	}
	setLokiObjectStorePrefix(storage, prefix)
	schemaFrom := defaultString(stringFromMap(existing, "schemaFrom"), sharedLokiSchemaFrom)
	deploymentMode := "SingleBinary"
	singleReplicas := 1
	readReplicas := 0
	writeReplicas := 0
	backendReplicas := 0
	replication := 1
	rateMB, burstMB, streams, queryLen, queryPar, bodySize := 1, 2, 5000, "7d", 8, "4m"
	if mode == sizerModeSimpleScalable {
		deploymentMode = "SimpleScalable"
		singleReplicas = 0
		readReplicas = 2
		writeReplicas = 2
		backendReplicas = 2
		replication = 2
		rateMB, burstMB, streams, queryLen, queryPar, bodySize = 2, 4, 20000, "30d", 32, "8m"
	}
	walSize := defaultString(req.WalStorageSize, "10Gi")
	persistence := map[string]any{"enabled": true, "size": walSize}
	if req.StorageClass != "" {
		persistence["storageClass"] = req.StorageClass
	}
	image := ""
	if h != nil {
		image = h.proxyImage
	}
	return map[string]any{
		"fullnameOverride": req.ReleaseName,
		"deploymentMode":   deploymentMode,
		"loki": map[string]any{
			"auth_enabled": true,
			"commonConfig": map[string]any{
				"replication_factor": replication,
				"path_prefix":        "/var/loki",
			},
			"schemaConfig": map[string]any{
				"configs": []any{
					map[string]any{
						"from":         schemaFrom,
						"store":        "tsdb",
						"object_store": "s3",
						"schema":       "v13",
						"index": map[string]any{
							"prefix": "loki_index_",
							"period": "24h",
						},
					},
				},
			},
			"storage": storage,
			"limits_config": map[string]any{
				"ingestion_rate_mb":           rateMB,
				"ingestion_burst_size_mb":     burstMB,
				"max_streams_per_user":        streams,
				"max_global_streams_per_user": streams,
				"max_line_size":               262144,
				"retention_period":            req.Retention,
				"max_query_length":            queryLen,
				"max_query_parallelism":       queryPar,
				"allow_structured_metadata":   true,
				"volume_enabled":              true,
			},
			"compactor": map[string]any{
				"retention_enabled":    true,
				"delete_request_store": "s3",
			},
		},
		"singleBinary": map[string]any{
			"replicas":    singleReplicas,
			"persistence": persistence,
			"resources": map[string]any{
				"requests": map[string]any{"cpu": "1000m", "memory": "2Gi"},
				"limits":   map[string]any{"cpu": "2000m", "memory": "4Gi"},
			},
		},
		"backend":      lokiComponentValues(backendReplicas, mode == sizerModeSimpleScalable, lokiSimpleScalablePodResources(), nil),
		"read":         lokiComponentValues(readReplicas, mode == sizerModeSimpleScalable, lokiSimpleScalablePodResources(), nil),
		"write":        lokiComponentValues(writeReplicas, mode == sizerModeSimpleScalable, lokiSimpleScalablePodResources(), persistence),
		"gateway":      lokiGatewayValues(mode, bodySize),
		"minio":        map[string]any{"enabled": false},
		"lokiCanary":   map[string]any{"enabled": false},
		"test":         map[string]any{"enabled": false},
		"chunksCache":  map[string]any{"enabled": false},
		"resultsCache": map[string]any{"enabled": false},
		"monitoring": map[string]any{
			"selfMonitoring": map[string]any{
				"enabled": false,
				"grafanaAgent": map[string]any{
					"installOperator": false,
				},
			},
			"lokiCanary": map[string]any{"enabled": false},
		},
		"extraObjects": lokiFamilyExtraObjects(req, image),
	}
}

func (h *MonitoringHandler) lokiIngestClass() string {
	if h == nil {
		return ""
	}
	return h.grafanaExpose.IngressClass
}

type lokiTokenHashLister interface {
	ListLokiIngestTokenHashes(ctx context.Context) ([]sqlc.ListLokiIngestTokenHashesRow, error)
}

func (h *MonitoringHandler) lokiHasIngestTokens(ctx context.Context) bool {
	if h == nil || h.queries == nil {
		return false
	}
	lister, ok := h.queries.(lokiTokenHashLister)
	if !ok {
		return false
	}
	rows, err := lister.ListLokiIngestTokenHashes(ctx)
	return err == nil && len(rows) > 0
}

func setLokiObjectStorePrefix(storage map[string]any, prefix string) {
	if storage == nil {
		return
	}
	obj, _ := storage["object_store"].(map[string]any)
	if obj == nil {
		obj = map[string]any{"type": "s3"}
		storage["object_store"] = obj
	}
	obj["prefix"] = prefix
}

func lokiSimpleScalablePodResources() map[string]any {
	// write/read/backend ×2 = 3000m / 6Gi; gateway adds 500m / 2Gi → 3500m / 8Gi chart.
	return map[string]any{
		"requests": map[string]any{"cpu": "500m", "memory": "1Gi"},
		"limits":   map[string]any{"cpu": "1000m", "memory": "2Gi"},
	}
}

func lokiSimpleScalableGatewayResources() map[string]any {
	return map[string]any{
		"requests": map[string]any{"cpu": "500m", "memory": "2Gi"},
		"limits":   map[string]any{"cpu": "1000m", "memory": "4Gi"},
	}
}

func lokiComponentValues(replicas int, withResources bool, resources map[string]any, persistence map[string]any) map[string]any {
	out := map[string]any{"replicas": replicas}
	if persistence != nil {
		out["persistence"] = persistence
	}
	if withResources {
		out["resources"] = resources
	}
	return out
}

func lokiGatewayValues(mode, bodySize string) map[string]any {
	out := map[string]any{
		"enabled": true,
		"service": map[string]any{"type": "ClusterIP"},
		"ingress": map[string]any{"enabled": false},
		"nginxConfig": map[string]any{
			"clientMaxBodySize": bodySize,
		},
	}
	if mode == sizerModeSimpleScalable {
		out["resources"] = lokiSimpleScalableGatewayResources()
	}
	return out
}

func lokiFamilyExtraObjects(req SharedLokiRequest, image string) []any {
	ns := defaultString(req.Namespace, "monitoring")
	release := defaultString(req.ReleaseName, sharedLokiDefaultRelease)
	_, authURL := lokiDerivedURLs(release, ns)
	gateway := fmt.Sprintf("http://%s-gateway.%s.svc.cluster.local", release, ns)
	labels := map[string]any{
		"app.kubernetes.io/name":      "loki-auth",
		"app.kubernetes.io/instance":  release,
		"app.kubernetes.io/component": "loki-auth",
	}
	svcName := release + "-auth"
	// Public ingest (Ingress/HTTPRoute/Certificate) is reconcile-owned so
	// Helm upgrades cannot fight a rotate-created object, and uninstall
	// can delete it even though Helm never adopted it.
	return []any{
		lokiAuthDeployment(ns, svcName, labels, image, gateway),
		lokiAuthService(ns, svcName, labels),
		lokiAuthListenNetworkPolicy(ns, svcName, labels),
		lokiGatewayFromAuthNetworkPolicy(ns, release, labels),
		grafanaDatasourceConfigMap(
			release+"-grafana-datasource",
			ns,
			release,
			lokiGrafanaDatasourceYAML(authURL),
		),
	}
}

func lokiAuthDeployment(namespace, name string, labels map[string]any, image, upstream string) map[string]any {
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    labels,
		},
		"spec": map[string]any{
			"replicas": 1,
			"selector": map[string]any{"matchLabels": labels},
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec": map[string]any{
					"automountServiceAccountToken": false,
					"securityContext": map[string]any{
						"runAsNonRoot": true,
						"runAsUser":    65534,
						"runAsGroup":   65534,
						"seccompProfile": map[string]any{
							"type": "RuntimeDefault",
						},
					},
					"containers": []any{
						map[string]any{
							"name":  "loki-auth",
							"image": image,
							"args":  []any{"loki-auth"},
							"ports": []any{
								map[string]any{"name": "http", "containerPort": lokiAuthListenPort},
							},
							"env": []any{
								map[string]any{"name": "LISTEN_ADDR", "value": fmt.Sprintf(":%d", lokiAuthListenPort)},
								map[string]any{"name": "LOKI_UPSTREAM", "value": upstream},
								map[string]any{"name": "HASHES_PATH", "value": "/var/run/loki-auth/hashes/hashes"},
								map[string]any{"name": "ACL_PATH", "value": "/var/run/loki-auth/acl/acl"},
							},
							"volumeMounts": []any{
								map[string]any{"name": "token-hashes", "mountPath": "/var/run/loki-auth/hashes", "readOnly": true},
								map[string]any{"name": "query-acl", "mountPath": "/var/run/loki-auth/acl", "readOnly": true},
							},
							"readinessProbe": map[string]any{
								"httpGet": map[string]any{"path": "/ready", "port": lokiAuthListenPort},
							},
							"resources": map[string]any{
								"requests": map[string]any{"cpu": "100m", "memory": "64Mi"},
								"limits":   map[string]any{"cpu": "200m", "memory": "128Mi"},
							},
							"securityContext": map[string]any{
								"allowPrivilegeEscalation": false,
								"readOnlyRootFilesystem":   true,
								"runAsNonRoot":             true,
								"capabilities":             map[string]any{"drop": []any{"ALL"}},
							},
						},
					},
					"volumes": []any{
						map[string]any{
							"name": "token-hashes",
							"secret": map[string]any{
								"secretName": lokiTokenHashSecretName,
								"optional":   true,
							},
						},
						map[string]any{
							"name": "query-acl",
							"configMap": map[string]any{
								"name":     lokiQueryACLConfigMapName,
								"optional": true,
							},
						},
					},
				},
			},
		},
	}
}

func lokiAuthService(namespace, name string, labels map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    labels,
		},
		"spec": map[string]any{
			"type":     "ClusterIP",
			"selector": labels,
			"ports": []any{
				map[string]any{"name": "http", "port": lokiAuthListenPort, "targetPort": lokiAuthListenPort},
			},
		},
	}
}

func lokiGrafanaDatasourceYAML(url string) string {
	return strings.TrimSpace(fmt.Sprintf(`
apiVersion: 1
datasources:
  - name: Loki
    uid: loki
    type: loki
    access: proxy
    url: %s
    editable: false
    jsonData:
      timeout: 60
      # Grafana dataproxy.send_user_header ships X-Grafana-User to loki-auth.
      # Dashboards set X-Scope-OrgID from var-cluster; loki-auth allow-lists it.
`, yamlQuotedString(url))) + "\n"
}

func lokiAuthListenNetworkPolicy(namespace, name string, labels map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name":      name + "-ingress",
			"namespace": namespace,
		},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": labels},
			"policyTypes": []any{"Ingress"},
			"ingress": []any{
				map[string]any{
					"ports": []any{
						map[string]any{"protocol": "TCP", "port": lokiAuthListenPort},
					},
				},
			},
		},
	}
}

func lokiGatewayFromAuthNetworkPolicy(namespace, release string, authLabels map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name":      release + "-gateway-from-auth",
			"namespace": namespace,
		},
		"spec": map[string]any{
			"podSelector": map[string]any{
				"matchLabels": map[string]any{
					"app.kubernetes.io/name":      "loki",
					"app.kubernetes.io/instance":  release,
					"app.kubernetes.io/component": "gateway",
				},
			},
			"policyTypes": []any{"Ingress"},
			"ingress": []any{
				map[string]any{
					"from": []any{
						map[string]any{"podSelector": map[string]any{"matchLabels": authLabels}},
					},
				},
			},
		},
	}
}

func lokiIngestShouldBePublic(status string, hasTokens bool, hostname string) bool {
	if !hasTokens || strings.TrimSpace(hostname) == "" {
		return false
	}
	switch status {
	case "", "not_configured", "uninstalled":
		return false
	default:
		return true
	}
}

func lokiTLSIssuer(expose GrafanaExpose) (name, kind string) {
	name = strings.TrimSpace(expose.TLSIssuerName)
	if name == "" {
		name = "astronomer-tls"
	}
	kind = strings.TrimSpace(expose.TLSIssuerKind)
	if kind == "" {
		kind = "Issuer"
	}
	return name, kind
}

func lokiIngestCertificate(namespace, host string, expose GrafanaExpose) map[string]any {
	issuer, kind := lokiTLSIssuer(expose)
	return map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]any{
			"name":      lokiIngestTLSSecretName,
			"namespace": namespace,
		},
		"spec": map[string]any{
			"secretName": lokiIngestTLSSecretName,
			"dnsNames":   []any{host},
			"issuerRef": map[string]any{
				"name":  issuer,
				"kind":  kind,
				"group": "cert-manager.io",
			},
		},
	}
}

func lokiIngestIngress(namespace, svcName, host, ingressClass string, expose GrafanaExpose) map[string]any {
	issuer, kind := lokiTLSIssuer(expose)
	annotations := map[string]any{}
	if strings.EqualFold(kind, "ClusterIssuer") {
		annotations["cert-manager.io/cluster-issuer"] = issuer
	} else {
		annotations["cert-manager.io/issuer"] = issuer
	}
	spec := map[string]any{
		"tls": []any{
			map[string]any{
				"hosts":      []any{host},
				"secretName": lokiIngestTLSSecretName,
			},
		},
		"rules": []any{
			map[string]any{
				"host": host,
				"http": map[string]any{
					"paths": []any{
						map[string]any{
							"path":     "/loki/api/v1/push",
							"pathType": "Prefix",
							"backend": map[string]any{
								"service": map[string]any{
									"name": svcName,
									"port": map[string]any{"number": lokiAuthListenPort},
								},
							},
						},
					},
				},
			},
		},
	}
	if ingressClass != "" {
		spec["ingressClassName"] = ingressClass
	}
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"name":        svcName,
			"namespace":   namespace,
			"annotations": annotations,
		},
		"spec": spec,
	}
}

func lokiIngestHTTPRoute(platformNS, gatewayName, lokiNS, svcName, host string) map[string]any {
	return map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata": map[string]any{
			"name":      svcName,
			"namespace": platformNS,
		},
		"spec": map[string]any{
			"parentRefs": []any{
				map[string]any{"name": gatewayName},
			},
			"hostnames": []any{host},
			"rules": []any{
				map[string]any{
					"matches": []any{
						map[string]any{
							"path": map[string]any{"type": "PathPrefix", "value": "/loki/api/v1/push"},
						},
					},
					"backendRefs": []any{
						map[string]any{
							"name":      svcName,
							"namespace": lokiNS,
							"port":      lokiAuthListenPort,
						},
					},
				},
			},
		},
	}
}

func (h *MonitoringHandler) stampSharedLokiHealth(ctx context.Context, req SharedLokiRequest) error {
	if h.queries == nil {
		return nil
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return err
	}
	return h.updateSharedLokiMetadata(ctx, backend, req, "healthy")
}
