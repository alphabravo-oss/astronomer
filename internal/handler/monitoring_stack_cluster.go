package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// The per-cluster monitoring stack: everything under /clusters/{id}/monitoring.
//
// WHY THERE IS NO AUTHORIZATION PREAMBLE IN THIS FILE. Its shared-stack sibling
// (monitoring_stack_shared.go) runs h.authz.authorizeGlobalAction as the first
// statement of every lifecycle body. These eight handlers deliberately do not,
// and must not be "fixed" to match: their gate is requirePermission middleware
// mounted per route at internal/server/routes_clusters.go, which is evaluated
// before the handler runs and carries a per-route verb the handler does not
// know (install is VerbCreate, uninstall is VerbDelete, where the shared family
// uses VerbUpdate for both). Adding a second in-handler check would either be
// redundant or quietly narrow the gate.
//
// That is also why this family is not a third instantiation of
// sharedStackLifecycle: an instantiation with no resource to check would need
// an optional "authorization happens elsewhere" field on the one type whose
// purpose is to make skipping the check impossible.
//
// The fence for these eight, in place of a preamble a reader can see, is two
// tests, and it takes both:
//
//   - TestClusterStackLifecycleDeniesCallerWithoutMonitoringPermission
//     (monitoring_stack_test.go) drives each handler through the same middleware
//     with the same (resource, verb) pairs the router mounts, and fails if any
//     of them admits a caller holding no monitoring grant. It proves the
//     handlers are safe BEHIND the gate; it cannot see whether the gate is
//     mounted.
//   - TestClusterMonitoringRoutesRequireMonitoringRBAC
//     (internal/server/routes_authz_deny_test.go) drives all eight real routes
//     through the real router as an authenticated caller holding every verb on
//     clusters and nothing on monitoring, and requires 403. That is what fails
//     if a route here loses its requirePermission wrapper.
//
// Nothing else would catch that. These routes sit under the `authenticated`
// group, so an anonymous request gets 401 from requireAuth whether or not
// requirePermission is present and TestEveryRouteDeniesUnauthenticatedRequests
// stays green; both chi.Walk callbacks in routes_security_test.go discard the
// middleware chain, and docs/route-risk-classifications.json is a static
// (method, pattern) map that never asserts what a route is wrapped in.

func (h *MonitoringHandler) GetClusterConfig(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondJSON(w, http.StatusOK, map[string]any{})
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cfg, err := h.queries.GetClusterMonitoringConfig(r.Context(), clusterID)
	if err != nil {
		if err == pgx.ErrNoRows {
			RespondJSON(w, http.StatusOK, map[string]any{})
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to load cluster monitoring config")
		return
	}
	RespondJSON(w, http.StatusOK, clusterMonitoringConfigResponse(cfg))
}

func (h *MonitoringHandler) UpdateClusterConfig(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MonitoringError, "monitoring store not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	var req UpdateClusterMonitoringConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	var backendID uuid.UUID
	if req.BackendID != nil {
		backendID = *req.BackendID
	} else {
		backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.MonitoringError, "Default monitoring backend is not configured")
			return
		}
		backendID = backend.ID
	}
	existing, hasExisting, _ := h.loadStackConfig(r.Context(), clusterID.String())
	lastAppliedSpecHash := ""
	lastObservedStatus := ""
	lastObservedRevision := int32(0)
	lastObservedAt := pgtype.Timestamptz{}
	lastDriftDetectedAt := pgtype.Timestamptz{}
	if hasExisting {
		lastAppliedSpecHash = existing.LastAppliedSpecHash
		lastObservedStatus = existing.LastObservedStatus
		lastObservedRevision = existing.LastObservedRevision
		lastObservedAt = existing.LastObservedAt
		lastDriftDetectedAt = existing.LastDriftDetectedAt
	}
	clusterCfg, err := h.queries.UpsertClusterMonitoringConfig(r.Context(), sqlc.UpsertClusterMonitoringConfigParams{
		ClusterID:               clusterID,
		BackendID:               backendID,
		ClusterLabel:            defaultString(req.ClusterLabel, "cluster_id"),
		ClusterLabelValue:       req.ClusterLabelValue,
		ScrapeIntervalSeconds:   defaultInt32(req.ScrapeIntervalSeconds, 30),
		Retention:               defaultString(req.Retention, "15d"),
		StackNamespace:          defaultString(req.StackNamespace, "monitoring"),
		PrometheusReleaseName:   defaultString(req.PrometheusReleaseName, "prometheus"),
		ThanosSidecarEnabled:    req.ThanosSidecarEnabled,
		StorageConfigID:         parseOptionalUUID(req.StorageConfigID),
		ObjectStorageSecretName: req.ObjectStorageSecretName,
		StorageClass:            req.StorageClass,
		StorageSize:             req.StorageSize,
		LastAppliedSpecHash:     lastAppliedSpecHash,
		LastObservedStatus:      lastObservedStatus,
		LastObservedRevision:    lastObservedRevision,
		LastObservedAt:          lastObservedAt,
		LastDriftDetectedAt:     lastDriftDetectedAt,
		Status:                  defaultString(req.Status, "configured"),
		LastHealthyAt:           nullableNow(req.Status == "healthy"),
		CreatedByID:             currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to save cluster monitoring config")
		return
	}
	recordAudit(r, h.queries, "monitoring.cluster_config.update", "cluster_monitoring_config", clusterCfg.ClusterID.String(), clusterCfg.PrometheusReleaseName, map[string]any{
		"backendId":      clusterCfg.BackendID.String(),
		"stackNamespace": clusterCfg.StackNamespace,
		"status":         clusterCfg.Status,
	})
	RespondJSON(w, http.StatusOK, clusterMonitoringConfigResponse(clusterCfg))
}

func (h *MonitoringHandler) PreviewStack(w http.ResponseWriter, r *http.Request) {
	clusterID, req, values, err := h.monitoringStackPayload(r.Context(), r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	cfg, ok, _ := h.loadStackConfig(r.Context(), clusterID)
	replaceRequired, reasons := clusterMonitoringReplaceRequired(cfg, ok, req)
	RespondJSON(w, http.StatusOK, map[string]any{
		"clusterId": clusterID,
		"chart": map[string]any{
			"repoUrl":   "https://prometheus-community.github.io/helm-charts",
			"chartName": "kube-prometheus-stack",
		},
		"values":          sanitizeMonitoringValues(values),
		"desiredSpecHash": specHash(values),
		"requiresReplace": replaceRequired,
		"replaceReasons":  reasons,
	})
}

func (h *MonitoringHandler) InstallStack(w http.ResponseWriter, r *http.Request) {
	clusterID, req, values, err := h.monitoringStackPayload(r.Context(), r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	if err := h.persistStackConfig(r.Context(), clusterID, req, "installing"); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to persist monitoring stack config")
		return
	}
	op, err := h.enqueueClusterStackOperation(withOperationIdempotency(r, "monitoring"), currentUserUUID(r), "install", clusterID, req, values)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to create monitoring operation")
		return
	}
	recordAudit(r, h.queries, "monitoring.stack.install", "cluster_monitoring_config", clusterID, req.ReleaseName, map[string]any{
		"namespace":   req.Namespace,
		"operationId": op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (h *MonitoringHandler) UpgradeStack(w http.ResponseWriter, r *http.Request) {
	clusterID, req, values, err := h.monitoringStackPayload(r.Context(), r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	cfg, ok, loadErr := h.loadStackConfig(r.Context(), clusterID)
	if loadErr != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, loadErr.Error())
		return
	}
	if replaceRequired, reasons := clusterMonitoringReplaceRequired(cfg, ok, req); replaceRequired {
		RespondJSON(w, http.StatusConflict, map[string]any{
			"error":           "replace_required",
			"message":         "Requested monitoring stack changes require reinstall rather than in-place upgrade",
			"requiresReplace": true,
			"replaceReasons":  reasons,
		})
		return
	}
	if err := h.persistStackConfig(r.Context(), clusterID, req, "updating"); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to persist monitoring stack config")
		return
	}
	op, err := h.enqueueClusterStackOperation(withOperationIdempotency(r, "monitoring"), currentUserUUID(r), "upgrade", clusterID, req, values)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to create monitoring operation")
		return
	}
	recordAudit(r, h.queries, "monitoring.stack.upgrade", "cluster_monitoring_config", clusterID, req.ReleaseName, map[string]any{
		"namespace":   req.Namespace,
		"operationId": op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (h *MonitoringHandler) ReplaceStack(w http.ResponseWriter, r *http.Request) {
	clusterID, req, values, err := h.monitoringStackPayload(r.Context(), r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	if _, _, loadErr := h.loadStackConfig(r.Context(), clusterID); loadErr != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, loadErr.Error())
		return
	}
	if err := h.persistStackConfig(r.Context(), clusterID, req, "reinstalled"); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to persist monitoring stack config")
		return
	}
	op, err := h.enqueueClusterStackOperation(withOperationIdempotency(r, "monitoring"), currentUserUUID(r), "replace", clusterID, req, values)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to create monitoring operation")
		return
	}
	recordAudit(r, h.queries, "monitoring.stack.replace", "cluster_monitoring_config", clusterID, req.ReleaseName, map[string]any{
		"namespace":   req.Namespace,
		"operationId": op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (h *MonitoringHandler) UninstallStack(w http.ResponseWriter, r *http.Request) {
	if h.helm == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.HelmError, "helm requester not configured")
		return
	}
	clusterID := chi.URLParam(r, "cluster_id")
	cfg, _, err := h.loadStackConfig(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	if h.queries != nil {
		clusterUUID, parseErr := uuid.Parse(clusterID)
		if parseErr == nil {
			_, _ = h.queries.UpsertClusterMonitoringConfig(r.Context(), sqlc.UpsertClusterMonitoringConfigParams{
				ClusterID:               clusterUUID,
				BackendID:               cfg.BackendID,
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
				LastObservedStatus:      "uninstalled",
				LastObservedRevision:    0,
				LastObservedAt:          pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
				LastDriftDetectedAt:     pgtype.Timestamptz{},
				Status:                  "uninstalled",
				LastHealthyAt:           pgtype.Timestamptz{},
				CreatedByID:             currentUserUUID(r),
			})
		}
	}
	op, err := h.enqueueClusterStackOperation(withOperationIdempotency(r, "monitoring"), currentUserUUID(r), "uninstall", clusterID, MonitoringStackRequest{
		ReleaseName: cfg.PrometheusReleaseName,
		Namespace:   cfg.StackNamespace,
	}, nil)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to create monitoring operation")
		return
	}
	recordAudit(r, h.queries, "monitoring.stack.uninstall", "cluster_monitoring_config", clusterID, cfg.PrometheusReleaseName, map[string]any{
		"namespace":   cfg.StackNamespace,
		"operationId": op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (h *MonitoringHandler) GetStackStatus(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	cfg, ok, err := h.loadStackConfig(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	if !ok {
		RespondJSON(w, http.StatusOK, map[string]any{"status": "not_configured"})
		return
	}
	status := map[string]any{
		"status":                  cfg.Status,
		"namespace":               cfg.StackNamespace,
		"releaseName":             cfg.PrometheusReleaseName,
		"retention":               cfg.Retention,
		"thanosSidecarEnabled":    cfg.ThanosSidecarEnabled,
		"storageConfigId":         nullableUUID(cfg.StorageConfigID),
		"objectStorageSecretName": cfg.ObjectStorageSecretName,
		"storageClass":            cfg.StorageClass,
		"storageSize":             cfg.StorageSize,
		"desiredSpecHash":         cfg.LastAppliedSpecHash,
		"lastObservedStatus":      cfg.LastObservedStatus,
		"lastObservedRevision":    cfg.LastObservedRevision,
		"lastObservedAt":          nullablePgTime(cfg.LastObservedAt),
		"lastDriftDetectedAt":     nullablePgTime(cfg.LastDriftDetectedAt),
		"lastHealthyAt":           nullablePgTime(cfg.LastHealthyAt),
	}
	if observed, drifted, reasons := h.observeRelease(r.Context(), clusterID, releaseRef{
		Namespace:   cfg.StackNamespace,
		ReleaseName: cfg.PrometheusReleaseName,
	}); observed != nil {
		status["observedRelease"] = observed
		status["drifted"] = drifted
		status["driftReasons"] = reasons
	}
	if op, ok := h.latestMonitoringOperation(r.Context(), "cluster_stack", clusterID); ok {
		status["operation"] = op
	}
	if h.requester != nil {
		path := fmt.Sprintf("/api/v1/namespaces/%s/pods?labelSelector=%s", cfg.StackNamespace, url.QueryEscape("app.kubernetes.io/instance="+cfg.PrometheusReleaseName))
		resp, doErr := h.requester.Do(r.Context(), clusterID, http.MethodGet, path, nil, requestHeaders(""))
		if doErr == nil && ensureSuccess(resp) == nil {
			var payload map[string]any
			if parseErr := parseJSONResponse(resp, &payload); parseErr == nil {
				status["pods"] = len(objectItems(payload))
			}
		}
	}
	RespondJSON(w, http.StatusOK, status)
}

func clusterMonitoringConfigResponse(cfg sqlc.ClusterMonitoringConfig) map[string]any {
	return map[string]any{
		"id":                      cfg.ID.String(),
		"clusterId":               cfg.ClusterID.String(),
		"backendId":               cfg.BackendID.String(),
		"clusterLabel":            cfg.ClusterLabel,
		"clusterLabelValue":       cfg.ClusterLabelValue,
		"scrapeIntervalSeconds":   cfg.ScrapeIntervalSeconds,
		"retention":               cfg.Retention,
		"stackNamespace":          cfg.StackNamespace,
		"prometheusReleaseName":   cfg.PrometheusReleaseName,
		"thanosSidecarEnabled":    cfg.ThanosSidecarEnabled,
		"storageConfigId":         nullableUUID(cfg.StorageConfigID),
		"objectStorageSecretName": cfg.ObjectStorageSecretName,
		"storageClass":            cfg.StorageClass,
		"storageSize":             cfg.StorageSize,
		"lastAppliedSpecHash":     cfg.LastAppliedSpecHash,
		"lastObservedStatus":      cfg.LastObservedStatus,
		"lastObservedRevision":    cfg.LastObservedRevision,
		"lastObservedAt":          nullablePgTime(cfg.LastObservedAt),
		"lastDriftDetectedAt":     nullablePgTime(cfg.LastDriftDetectedAt),
		"status":                  cfg.Status,
		"lastHealthyAt":           nullablePgTime(cfg.LastHealthyAt),
	}
}

func (h *MonitoringHandler) monitoringStackPayload(ctx context.Context, r *http.Request) (string, MonitoringStackRequest, map[string]any, error) {
	clusterID := chi.URLParam(r, "cluster_id")
	var req MonitoringStackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		return "", MonitoringStackRequest{}, nil, fmt.Errorf("invalid JSON body")
	}
	if req.ReleaseName == "" {
		req.ReleaseName = "prometheus"
	}
	if req.Namespace == "" {
		req.Namespace = "monitoring"
	}
	if req.Retention == "" {
		req.Retention = "15d"
	}
	if req.StorageSize == "" {
		req.StorageSize = "50Gi"
	}
	if req.StorageClass == "" {
		req.StorageClass = "default"
	}
	if req.ScrapeInterval == "" {
		req.ScrapeInterval = "30s"
	}
	if req.ClusterLabel == "" {
		req.ClusterLabel = "cluster_id"
	}
	if req.ClusterLabelValue == "" {
		req.ClusterLabelValue = clusterID
	}
	if req.ChartVersion == "" {
		req.ChartVersion = "61.3.2"
	}
	enableGrafana := true
	if req.EnableGrafana != nil {
		enableGrafana = *req.EnableGrafana
	}
	enableAlertmanager := true
	if req.EnableAlertmanager != nil {
		enableAlertmanager = *req.EnableAlertmanager
	}
	enableSidecar := true
	if req.ThanosSidecarEnabled != nil {
		enableSidecar = *req.ThanosSidecarEnabled
	}
	values := map[string]any{
		"grafana": map[string]any{
			"enabled": enableGrafana,
		},
		"alertmanager": map[string]any{
			"enabled": enableAlertmanager,
			"alertmanagerSpec": map[string]any{
				"replicas": 1,
			},
		},
		"prometheus": map[string]any{
			"prometheusSpec": map[string]any{
				"retention":      req.Retention,
				"externalLabels": map[string]any{req.ClusterLabel: req.ClusterLabelValue},
				"scrapeInterval": req.ScrapeInterval,
				"enableAdminAPI": false,
				"storageSpec": map[string]any{
					"volumeClaimTemplate": map[string]any{
						"spec": map[string]any{
							"storageClassName": req.StorageClass,
							"accessModes":      []string{"ReadWriteOnce"},
							"resources": map[string]any{
								"requests": map[string]any{
									"storage": req.StorageSize,
								},
							},
						},
					},
				},
				"thanos": map[string]any{
					"baseImage": "quay.io/thanos/thanos",
					"version":   "v0.36.1",
				},
			},
		},
	}
	if enableSidecar && req.StorageConfigID != "" {
		secretSpec, err := h.objectStoreSecretSpec(ctx, req.StorageConfigID, req.ObjectStorageSecretName, req.ReleaseName+"-thanos-objstore")
		if err != nil {
			return "", MonitoringStackRequest{}, nil, err
		}
		req.ObjectStorageSecretName = secretSpec.Name
		values["prometheus"].(map[string]any)["prometheusSpec"].(map[string]any)["thanos"].(map[string]any)["objectStorageConfig"] = map[string]any{
			"existingSecret": secretSpec.Name,
			"key":            secretSpec.Key,
		}
	}
	if !enableSidecar {
		delete(values["prometheus"].(map[string]any)["prometheusSpec"].(map[string]any), "thanos")
	}
	return clusterID, req, values, nil
}

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

func parseOptionalUUID(raw string) pgtype.UUID {
	if raw == "" {
		return pgtype.UUID{}
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

func (h *MonitoringHandler) persistStackConfig(ctx context.Context, clusterID string, req MonitoringStackRequest, status string) error {
	if h.queries == nil {
		return nil
	}
	clusterUUID, err := uuid.Parse(clusterID)
	if err != nil {
		return err
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return err
	}
	appliedHash := specHash(map[string]any{
		"clusterID":               clusterID,
		"clusterLabel":            req.ClusterLabel,
		"clusterLabelValue":       req.ClusterLabelValue,
		"namespace":               req.Namespace,
		"releaseName":             req.ReleaseName,
		"retention":               req.Retention,
		"storageConfigID":         req.StorageConfigID,
		"objectStorageSecretName": req.ObjectStorageSecretName,
		"storageClass":            req.StorageClass,
		"storageSize":             req.StorageSize,
		"scrapeInterval":          req.ScrapeInterval,
		"chartVersion":            req.ChartVersion,
		"thanosSidecarEnabled":    req.ThanosSidecarEnabled == nil || *req.ThanosSidecarEnabled,
		"autoRollbackOnFailure":   boolPtrValue(req.AutoRollbackOnFailure),
	})
	_, err = h.queries.UpsertClusterMonitoringConfig(ctx, sqlc.UpsertClusterMonitoringConfigParams{
		ClusterID:               clusterUUID,
		BackendID:               backend.ID,
		ClusterLabel:            req.ClusterLabel,
		ClusterLabelValue:       req.ClusterLabelValue,
		ScrapeIntervalSeconds:   scrapeIntervalSeconds(req.ScrapeInterval),
		Retention:               req.Retention,
		StackNamespace:          req.Namespace,
		PrometheusReleaseName:   req.ReleaseName,
		ThanosSidecarEnabled:    req.ThanosSidecarEnabled == nil || *req.ThanosSidecarEnabled,
		StorageConfigID:         parseOptionalUUID(req.StorageConfigID),
		ObjectStorageSecretName: req.ObjectStorageSecretName,
		StorageClass:            req.StorageClass,
		StorageSize:             req.StorageSize,
		LastAppliedSpecHash:     appliedHash,
		LastObservedStatus:      "",
		LastObservedRevision:    0,
		LastObservedAt:          pgtype.Timestamptz{},
		LastDriftDetectedAt:     pgtype.Timestamptz{},
		Status:                  status,
		LastHealthyAt:           pgtype.Timestamptz{},
		CreatedByID:             pgtype.UUID{},
	})
	return err
}

func (h *MonitoringHandler) loadStackConfig(ctx context.Context, clusterID string) (sqlc.ClusterMonitoringConfig, bool, error) {
	if h.queries == nil {
		return sqlc.ClusterMonitoringConfig{}, false, nil
	}
	clusterUUID, err := uuid.Parse(clusterID)
	if err != nil {
		return sqlc.ClusterMonitoringConfig{}, false, err
	}
	cfg, err := h.queries.GetClusterMonitoringConfig(ctx, clusterUUID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return sqlc.ClusterMonitoringConfig{}, false, nil
		}
		return sqlc.ClusterMonitoringConfig{}, false, err
	}
	return cfg, true, nil
}
