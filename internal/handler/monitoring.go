package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type MonitoringHandler struct {
	requester K8sRequester
	queries   MonitoringQuerier
	helm      HelmRequester
	log       *slog.Logger
	authz     authorizationSupport
	mu        sync.Mutex
	triggerCh chan struct{}
}

type MonitoringQuerier interface {
	GetClusterHealthStatus(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterHealthStatus, error)
	GetDefaultMonitoringBackend(ctx context.Context) (sqlc.MonitoringBackend, error)
	GetClusterMonitoringConfig(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterMonitoringConfig, error)
	GetClusterMonitoringContext(ctx context.Context, clusterID uuid.UUID) (sqlc.GetClusterMonitoringContextRow, error)
	UpsertDefaultMonitoringBackend(ctx context.Context, arg sqlc.UpsertDefaultMonitoringBackendParams) (sqlc.MonitoringBackend, error)
	UpsertClusterMonitoringConfig(ctx context.Context, arg sqlc.UpsertClusterMonitoringConfigParams) (sqlc.ClusterMonitoringConfig, error)
	CreateMonitoringOperation(ctx context.Context, arg sqlc.CreateMonitoringOperationParams) (sqlc.MonitoringOperation, error)
	GetLatestMonitoringOperationForTarget(ctx context.Context, arg sqlc.GetLatestMonitoringOperationForTargetParams) (sqlc.MonitoringOperation, error)
	GetMonitoringOperation(ctx context.Context, id uuid.UUID) (sqlc.MonitoringOperation, error)
	ListMonitoringOperations(ctx context.Context, arg sqlc.ListMonitoringOperationsParams) ([]sqlc.MonitoringOperation, error)
	ListMonitoringOperationEvents(ctx context.Context, operationID uuid.UUID) ([]sqlc.MonitoringOperationEvent, error)
	ListPendingMonitoringOperations(ctx context.Context, limit int32) ([]sqlc.MonitoringOperation, error)
	MarkMonitoringOperationRunning(ctx context.Context, id uuid.UUID) (sqlc.MonitoringOperation, error)
	MarkMonitoringOperationCompleted(ctx context.Context, id uuid.UUID) (sqlc.MonitoringOperation, error)
	MarkMonitoringOperationFailed(ctx context.Context, arg sqlc.MarkMonitoringOperationFailedParams) (sqlc.MonitoringOperation, error)
	MarkMonitoringOperationSuperseded(ctx context.Context, arg sqlc.MarkMonitoringOperationSupersededParams) (sqlc.MonitoringOperation, error)
	RequeueMonitoringOperation(ctx context.Context, id uuid.UUID) (sqlc.MonitoringOperation, error)
	CreateMonitoringOperationEvent(ctx context.Context, arg sqlc.CreateMonitoringOperationEventParams) (sqlc.MonitoringOperationEvent, error)
	GetBackupStorageConfigByID(ctx context.Context, id uuid.UUID) (sqlc.BackupStorageConfig, error)
	ListNotificationChannels(ctx context.Context, arg sqlc.ListNotificationChannelsParams) ([]sqlc.NotificationChannel, error)
	ListAlertRules(ctx context.Context, arg sqlc.ListAlertRulesParams) ([]sqlc.AlertRule, error)
	ListChannelsForAlertRule(ctx context.Context, alertRuleID uuid.UUID) ([]sqlc.NotificationChannel, error)
}

func NewMonitoringHandler() *MonitoringHandler {
	return &MonitoringHandler{log: slog.Default(), triggerCh: make(chan struct{}, 1)}
}

func NewMonitoringHandlerWithRequester(requester K8sRequester) *MonitoringHandler {
	return &MonitoringHandler{requester: requester, log: slog.Default(), triggerCh: make(chan struct{}, 1)}
}

func NewMonitoringHandlerWithQueries(queries MonitoringQuerier, requester K8sRequester) *MonitoringHandler {
	return &MonitoringHandler{queries: queries, requester: requester, log: slog.Default(), triggerCh: make(chan struct{}, 1)}
}

func NewMonitoringHandlerWithDeps(queries MonitoringQuerier, requester K8sRequester, helm HelmRequester) *MonitoringHandler {
	return &MonitoringHandler{queries: queries, requester: requester, helm: helm, log: slog.Default(), triggerCh: make(chan struct{}, 1)}
}

func (h *MonitoringHandler) PrometheusQuery(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	if summary, ok, err := h.realClusterSummary(r.Context(), clusterID); err != nil {
		RespondError(w, http.StatusServiceUnavailable, "metrics_error", err.Error())
		return
	} else if ok {
		RespondJSON(w, http.StatusOK, summary)
		return
	}
	summary, err := h.clusterSummary(r.Context(), clusterID)
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "metrics_error", err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, summary)
}

func (h *MonitoringHandler) PrometheusQueryRange(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	name := chi.URLParam(r, "name")
	namespace := chi.URLParam(r, "namespace")
	kind := chi.URLParam(r, "kind")
	if data, ok, err := h.realWorkloadMetrics(r.Context(), clusterID, kind, namespace, name, r.URL.Query().Get("range")); err != nil {
		RespondError(w, http.StatusServiceUnavailable, "metrics_error", err.Error())
		return
	} else if ok {
		RespondJSON(w, http.StatusOK, data)
		return
	}
	summary, err := h.workloadSummary(r.Context(), clusterID, kind, namespace, name)
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "metrics_error", err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, h.metricsSeries(summary, r.URL.Query().Get("range"), namespace+"/"+name))
}

func (h *MonitoringHandler) ListMetrics(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	if r.URL.Path == "/api/v1/clusters/"+clusterID+"/metrics/summary/" {
		if summary, ok, err := h.realClusterSummary(r.Context(), clusterID); err != nil {
			RespondError(w, http.StatusServiceUnavailable, "metrics_error", err.Error())
			return
		} else if ok {
			RespondJSON(w, http.StatusOK, summary)
			return
		}
	} else {
		if data, ok, err := h.realClusterMetrics(r.Context(), clusterID, r.URL.Query().Get("range")); err != nil {
			RespondError(w, http.StatusServiceUnavailable, "metrics_error", err.Error())
			return
		} else if ok {
			RespondJSON(w, http.StatusOK, data)
			return
		}
	}
	summary, err := h.clusterSummary(r.Context(), clusterID)
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "metrics_error", err.Error())
		return
	}

	if r.URL.Path == "/api/v1/clusters/"+clusterID+"/metrics/summary/" {
		RespondJSON(w, http.StatusOK, summary)
		return
	}

	RespondJSON(w, http.StatusOK, h.metricsSeries(summary, r.URL.Query().Get("range"), "cluster"))
}

type UpdateMonitoringBackendRequest struct {
	BackendType                  string          `json:"backendType"`
	QueryURL                     string          `json:"queryUrl"`
	AlertmanagerURL              string          `json:"alertmanagerUrl"`
	TenantID                     string          `json:"tenantId"`
	AuthType                     string          `json:"authType"`
	AuthConfig                   json.RawMessage `json:"authConfig"`
	DefaultStepSeconds           int32           `json:"defaultStepSeconds"`
	TimeoutSeconds               int32           `json:"timeoutSeconds"`
	DefaultAutoRollbackOnFailure *bool           `json:"defaultAutoRollbackOnFailure"`
	MaxRetryAttempts             int32           `json:"maxRetryAttempts"`
}

type UpdateClusterMonitoringConfigRequest struct {
	BackendID               *uuid.UUID `json:"backendId"`
	ClusterLabel            string     `json:"clusterLabel"`
	ClusterLabelValue       string     `json:"clusterLabelValue"`
	ScrapeIntervalSeconds   int32      `json:"scrapeIntervalSeconds"`
	Retention               string     `json:"retention"`
	StackNamespace          string     `json:"stackNamespace"`
	PrometheusReleaseName   string     `json:"prometheusReleaseName"`
	ThanosSidecarEnabled    bool       `json:"thanosSidecarEnabled"`
	StorageConfigID         string     `json:"storageConfigId"`
	ObjectStorageSecretName string     `json:"objectStorageSecretName"`
	StorageClass            string     `json:"storageClass"`
	StorageSize             string     `json:"storageSize"`
	Status                  string     `json:"status"`
}

type MonitoringStackRequest struct {
	ReleaseName             string `json:"releaseName"`
	Namespace               string `json:"namespace"`
	Retention               string `json:"retention"`
	StorageClass            string `json:"storageClass"`
	StorageSize             string `json:"storageSize"`
	ScrapeInterval          string `json:"scrapeInterval"`
	ClusterLabel            string `json:"clusterLabel"`
	ClusterLabelValue       string `json:"clusterLabelValue"`
	PrometheusVersion       string `json:"prometheusVersion"`
	ChartVersion            string `json:"chartVersion"`
	StorageConfigID         string `json:"storageConfigId"`
	ObjectStorageSecretName string `json:"objectStorageSecretName"`
	EnableGrafana           *bool  `json:"enableGrafana"`
	EnableAlertmanager      *bool  `json:"enableAlertmanager"`
	ThanosSidecarEnabled    *bool  `json:"thanosSidecarEnabled"`
	AutoRollbackOnFailure   *bool  `json:"autoRollbackOnFailure"`
}

type SharedThanosStackRequest struct {
	ManagementClusterID     string `json:"managementClusterId"`
	Namespace               string `json:"namespace"`
	ReleaseName             string `json:"releaseName"`
	ChartVersion            string `json:"chartVersion"`
	StorageConfigID         string `json:"storageConfigId"`
	ObjectStorageSecretName string `json:"objectStorageSecretName"`
	QueryReplicas           int32  `json:"queryReplicas"`
	StoreGatewayReplicas    int32  `json:"storeGatewayReplicas"`
	CompactorReplicas       int32  `json:"compactorReplicas"`
	AutoRollbackOnFailure   *bool  `json:"autoRollbackOnFailure"`
}

type SharedAlertmanagerRequest struct {
	ManagementClusterID   string `json:"managementClusterId"`
	Namespace             string `json:"namespace"`
	ReleaseName           string `json:"releaseName"`
	ChartVersion          string `json:"chartVersion"`
	Replicas              int32  `json:"replicas"`
	StorageClass          string `json:"storageClass"`
	StorageSize           string `json:"storageSize"`
	AutoRollbackOnFailure *bool  `json:"autoRollbackOnFailure"`
}

type objectStoreSecretSpec struct {
	Name            string
	Key             string
	Content         string
	StorageConfigID string
}

type releaseRef struct {
	Namespace   string
	ReleaseName string
}

type monitoringOperationEnvelope struct {
	ClusterID                string                 `json:"clusterId,omitempty"`
	Request                  json.RawMessage        `json:"request,omitempty"`
	Values                   map[string]any         `json:"values,omitempty"`
	SecretSpec               *objectStoreSecretSpec `json:"secretSpec,omitempty"`
	ResolvedAutoRollback     bool                   `json:"resolvedAutoRollback"`
	ResolvedMaxRetryAttempts int32                  `json:"resolvedMaxRetryAttempts"`
}

func (h *MonitoringHandler) SetLogger(log *slog.Logger) {
	if log != nil {
		h.log = log
	}
}

func (h *MonitoringHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	h.authz.SetAuthorization(engine, querier)
}

func (h *MonitoringHandler) TriggerReconcile() {
	if h == nil || h.triggerCh == nil {
		return
	}
	select {
	case h.triggerCh <- struct{}{}:
	default:
	}
}

func (h *MonitoringHandler) StartReconciler(ctx context.Context) {
	if h == nil || h.queries == nil || h.helm == nil {
		return
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	go h.runReconciler(ctx)
}

func (h *MonitoringHandler) ListOperations(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondJSON(w, http.StatusOK, map[string]any{"data": []any{}})
		return
	}
	limit := int32(queryInt(r, "limit", 50))
	offset := int32(queryInt(r, "offset", 0))
	arg := sqlc.ListMonitoringOperationsParams{
		Limit:  limit,
		Offset: offset,
	}
	if v := strings.TrimSpace(r.URL.Query().Get("targetType")); v != "" {
		arg.TargetType = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("targetKey")); v != "" {
		arg.TargetKey = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		arg.Status = pgtype.Text{String: v, Valid: true}
	}
	items, err := h.queries.ListMonitoringOperations(r.Context(), arg)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to list monitoring operations")
		return
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "permission_error", "Failed to retrieve user permissions")
		return
	}
	resp := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if restricted {
			allowed, err := h.canReadMonitoringOperation(r.Context(), bindings, item)
			if err != nil || !allowed {
				continue
			}
		}
		resp = append(resp, monitoringOperationResponse(item))
	}
	RespondJSON(w, http.StatusOK, map[string]any{"data": resp, "limit": limit, "offset": offset})
}

func (h *MonitoringHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "monitoring_error", "monitoring store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid operation ID")
		return
	}
	op, err := h.queries.GetMonitoringOperation(r.Context(), id)
	if err != nil {
		if err == pgx.ErrNoRows {
			RespondError(w, http.StatusNotFound, "not_found", "Monitoring operation not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to load monitoring operation")
		return
	}
	if !h.authorizeMonitoringOperationRead(w, r, op) {
		return
	}
	resp := monitoringOperationResponse(op)
	if events, err := h.queries.ListMonitoringOperationEvents(r.Context(), op.ID); err == nil {
		resp["events"] = monitoringOperationEventsResponse(events)
	}
	RespondJSON(w, http.StatusOK, resp)
}

func (h *MonitoringHandler) RetryOperation(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "monitoring_error", "monitoring store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid operation ID")
		return
	}
	op, err := h.queries.GetMonitoringOperation(r.Context(), id)
	if err != nil {
		if err == pgx.ErrNoRows {
			RespondError(w, http.StatusNotFound, "not_found", "Monitoring operation not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to load monitoring operation")
		return
	}
	if op.Status != "failed" && op.Status != "superseded" {
		RespondError(w, http.StatusConflict, "invalid_state", "Only failed or superseded operations can be retried")
		return
	}
	requeued, err := h.queries.RequeueMonitoringOperation(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to requeue monitoring operation")
		return
	}
	h.TriggerReconcile()
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(requeued))
}

func (h *MonitoringHandler) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	h.processPendingMonitoringOperations(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.processPendingMonitoringOperations(ctx)
		case <-h.triggerCh:
			h.processPendingMonitoringOperations(ctx)
		}
	}
}

func (h *MonitoringHandler) GetBackendConfig(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondJSON(w, http.StatusOK, map[string]any{})
		return
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil {
		if err == pgx.ErrNoRows {
			RespondJSON(w, http.StatusOK, map[string]any{})
			return
		}
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to load monitoring backend")
		return
	}
	RespondJSON(w, http.StatusOK, monitoringBackendResponse(backend))
}

func (h *MonitoringHandler) UpdateBackendConfig(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "monitoring_error", "monitoring store not configured")
		return
	}
	var req UpdateMonitoringBackendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	authConfig := req.AuthConfig
	if authConfig == nil {
		authConfig = json.RawMessage(`{}`)
	}
	authConfigMap := decodeJSONMap(authConfig)
	policies := mapFromMapValue(authConfigMap["operationPolicies"])
	if req.DefaultAutoRollbackOnFailure != nil {
		policies["defaultAutoRollbackOnFailure"] = *req.DefaultAutoRollbackOnFailure
	}
	if req.MaxRetryAttempts > 0 {
		policies["maxRetryAttempts"] = req.MaxRetryAttempts
	} else if _, ok := policies["maxRetryAttempts"]; !ok {
		policies["maxRetryAttempts"] = int32(1)
	}
	authConfigMap["operationPolicies"] = policies
	rawAuthConfig, err := json.Marshal(authConfigMap)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid authConfig")
		return
	}
	if req.BackendType == "" {
		req.BackendType = "thanos"
	}
	if req.QueryURL == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "queryUrl is required")
		return
	}
	backend, err := h.queries.UpsertDefaultMonitoringBackend(r.Context(), sqlc.UpsertDefaultMonitoringBackendParams{
		BackendType:        req.BackendType,
		QueryUrl:           req.QueryURL,
		AlertmanagerUrl:    req.AlertmanagerURL,
		TenantID:           req.TenantID,
		AuthType:           req.AuthType,
		AuthConfig:         rawAuthConfig,
		DefaultStepSeconds: req.DefaultStepSeconds,
		TimeoutSeconds:     req.TimeoutSeconds,
		CreatedByID:        currentUserUUID(r),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to save monitoring backend")
		return
	}
	// UpdateBackendConfig is the upsert behind both CreateEndpoint and
	// UpdateEndpoint; we record the action as "update" — when the row didn't
	// exist before this is effectively a "create", but distinguishing the two
	// would require a pre-read and isn't worth the extra round-trip.
	recordAudit(r, h.queries, "monitoring.endpoint.update", "monitoring_backend", backend.ID.String(), backend.BackendType, map[string]any{
		"query_url":        backend.QueryUrl,
		"alertmanager_url": backend.AlertmanagerUrl,
		"tenant_id":        backend.TenantID,
		"auth_type":        backend.AuthType,
	})
	RespondJSON(w, http.StatusOK, monitoringBackendResponse(backend))
}

func (h *MonitoringHandler) PreviewSharedThanosStack(w http.ResponseWriter, r *http.Request) {
	req, values, _, backend, err := h.sharedThanosPayload(r.Context(), r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	replaceRequired, reasons := sharedThanosReplaceRequired(sharedThanosMetadata(backend), req)
	RespondJSON(w, http.StatusOK, map[string]any{
		"clusterId": req.ManagementClusterID,
		"chart": map[string]any{
			"repoUrl":   "https://stevehipwell.github.io/helm-charts/",
			"chartName": "thanos",
		},
		"values":          sanitizeMonitoringValues(values),
		"desiredSpecHash": specHash(values),
		"requiresReplace": replaceRequired,
		"replaceReasons":  reasons,
	})
}

func (h *MonitoringHandler) InstallSharedThanosStack(w http.ResponseWriter, r *http.Request) {
	req, values, secretSpec, backend, err := h.sharedThanosPayload(r.Context(), r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := h.updateSharedThanosMetadata(r.Context(), backend, req, "installing"); err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to persist shared Thanos metadata")
		return
	}
	op, err := h.enqueueSharedThanosOperation(r.Context(), currentUserUUID(r), "install", req, values, &secretSpec)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to create monitoring operation")
		return
	}
	recordAudit(r, h.queries, "monitoring.shared_thanos.install", "monitoring_backend", backend.ID.String(), backend.BackendType, map[string]any{
		"managementClusterId": req.ManagementClusterID,
		"namespace":           req.Namespace,
		"releaseName":         req.ReleaseName,
		"operationId":         op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (h *MonitoringHandler) UpgradeSharedThanosStack(w http.ResponseWriter, r *http.Request) {
	req, values, secretSpec, backend, err := h.sharedThanosPayload(r.Context(), r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if replaceRequired, reasons := sharedThanosReplaceRequired(sharedThanosMetadata(backend), req); replaceRequired {
		RespondJSON(w, http.StatusConflict, map[string]any{
			"error":           "replace_required",
			"message":         "Requested Thanos changes require reinstall rather than in-place upgrade",
			"requiresReplace": true,
			"replaceReasons":  reasons,
		})
		return
	}
	if err := h.updateSharedThanosMetadata(r.Context(), backend, req, "updating"); err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to persist shared Thanos metadata")
		return
	}
	op, err := h.enqueueSharedThanosOperation(r.Context(), currentUserUUID(r), "upgrade", req, values, &secretSpec)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to create monitoring operation")
		return
	}
	recordAudit(r, h.queries, "monitoring.shared_thanos.upgrade", "monitoring_backend", backend.ID.String(), backend.BackendType, map[string]any{
		"managementClusterId": req.ManagementClusterID,
		"namespace":           req.Namespace,
		"releaseName":         req.ReleaseName,
		"operationId":         op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (h *MonitoringHandler) ReplaceSharedThanosStack(w http.ResponseWriter, r *http.Request) {
	req, values, secretSpec, backend, err := h.sharedThanosPayload(r.Context(), r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	metadata := sharedThanosMetadata(backend)
	clusterID := defaultString(req.ManagementClusterID, stringFromMap(metadata, "managementClusterId"))
	namespace := defaultString(req.Namespace, defaultString(stringFromMap(metadata, "namespace"), "monitoring"))
	releaseName := defaultString(req.ReleaseName, defaultString(stringFromMap(metadata, "releaseName"), "thanos"))
	if clusterID == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "managementClusterId is required")
		return
	}
	if err := h.updateSharedThanosMetadata(r.Context(), backend, req, "reinstalled"); err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to persist shared Thanos metadata")
		return
	}
	op, err := h.enqueueSharedThanosOperation(r.Context(), currentUserUUID(r), "replace", SharedThanosStackRequest{
		ManagementClusterID:     clusterID,
		Namespace:               namespace,
		ReleaseName:             releaseName,
		ChartVersion:            req.ChartVersion,
		StorageConfigID:         req.StorageConfigID,
		ObjectStorageSecretName: req.ObjectStorageSecretName,
		QueryReplicas:           req.QueryReplicas,
		StoreGatewayReplicas:    req.StoreGatewayReplicas,
		CompactorReplicas:       req.CompactorReplicas,
	}, values, &secretSpec)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to create monitoring operation")
		return
	}
	recordAudit(r, h.queries, "monitoring.shared_thanos.replace", "monitoring_backend", backend.ID.String(), backend.BackendType, map[string]any{
		"managementClusterId": clusterID,
		"namespace":           namespace,
		"releaseName":         releaseName,
		"operationId":         op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (h *MonitoringHandler) UninstallSharedThanosStack(w http.ResponseWriter, r *http.Request) {
	if h.helm == nil || h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "helm_error", "monitoring deployment is not configured")
		return
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil {
		RespondError(w, http.StatusBadRequest, "monitoring_error", "Default monitoring backend is not configured")
		return
	}
	metadata := sharedThanosMetadata(backend)
	clusterID := r.URL.Query().Get("clusterId")
	if clusterID == "" {
		clusterID = stringFromMap(metadata, "managementClusterId")
	}
	if clusterID == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "clusterId is required")
		return
	}
	namespace := defaultString(stringFromMap(metadata, "namespace"), "monitoring")
	releaseName := defaultString(stringFromMap(metadata, "releaseName"), "thanos")
	if err := h.updateSharedThanosMetadata(r.Context(), backend, SharedThanosStackRequest{
		ManagementClusterID: clusterID,
		Namespace:           namespace,
		ReleaseName:         releaseName,
	}, "uninstalled"); err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to persist shared Thanos metadata")
		return
	}
	op, err := h.enqueueSharedThanosOperation(r.Context(), currentUserUUID(r), "uninstall", SharedThanosStackRequest{
		ManagementClusterID: clusterID,
		Namespace:           namespace,
		ReleaseName:         releaseName,
	}, nil, nil)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to create monitoring operation")
		return
	}
	recordAudit(r, h.queries, "monitoring.shared_thanos.uninstall", "monitoring_backend", backend.ID.String(), backend.BackendType, map[string]any{
		"managementClusterId": clusterID,
		"namespace":           namespace,
		"releaseName":         releaseName,
		"operationId":         op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (h *MonitoringHandler) GetSharedThanosStatus(w http.ResponseWriter, r *http.Request) {
	if !h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbRead) {
		return
	}
	if h.queries == nil {
		RespondJSON(w, http.StatusOK, map[string]any{"status": "not_configured"})
		return
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil {
		RespondJSON(w, http.StatusOK, map[string]any{"status": "not_configured"})
		return
	}
	metadata := sharedThanosMetadata(backend)
	status := map[string]any{
		"status":                  defaultString(stringFromMap(metadata, "status"), "not_configured"),
		"managementClusterId":     stringFromMap(metadata, "managementClusterId"),
		"namespace":               stringFromMap(metadata, "namespace"),
		"releaseName":             stringFromMap(metadata, "releaseName"),
		"storageConfigId":         stringFromMap(metadata, "storageConfigId"),
		"objectStorageSecretName": stringFromMap(metadata, "objectStorageSecretName"),
		"chartVersion":            stringFromMap(metadata, "chartVersion"),
		"queryReplicas":           metadata["queryReplicas"],
		"storeGatewayReplicas":    metadata["storeGatewayReplicas"],
		"compactorReplicas":       metadata["compactorReplicas"],
		"desiredSpecHash":         stringFromMap(metadata, "lastAppliedSpecHash"),
		"managedAssetHashes":      mapFromMapValue(metadata["managedAssetHashes"]),
		"alertingAssetHashes":     mapFromMapValue(mapFromMapValue(decodeJSONMap(backend.AuthConfig)["sharedAlertingAssets"])["hashes"]),
	}
	if observed, drifted, reasons := h.observeRelease(r.Context(), stringFromMap(metadata, "managementClusterId"), releaseRef{
		Namespace:   defaultString(stringFromMap(metadata, "namespace"), "monitoring"),
		ReleaseName: defaultString(stringFromMap(metadata, "releaseName"), "thanos"),
	}); observed != nil {
		status["observedRelease"] = observed
		status["drifted"] = drifted
		status["driftReasons"] = reasons
		if drifted && status["status"] == "healthy" {
			status["status"] = "drifted"
		}
	}
	if op, ok := h.latestMonitoringOperation(r.Context(), "shared_thanos", "shared"); ok {
		status["operation"] = op
	}
	if h.requester != nil {
		clusterID := stringFromMap(metadata, "managementClusterId")
		namespace := defaultString(stringFromMap(metadata, "namespace"), "monitoring")
		releaseName := defaultString(stringFromMap(metadata, "releaseName"), "thanos")
		if clusterID != "" {
			path := fmt.Sprintf("/api/v1/namespaces/%s/pods?labelSelector=%s", namespace, url.QueryEscape("app.kubernetes.io/instance="+releaseName))
			resp, doErr := h.requester.Do(r.Context(), clusterID, http.MethodGet, path, nil, requestHeaders(""))
			if doErr == nil && ensureSuccess(resp) == nil {
				var payload map[string]any
				if parseErr := parseJSONResponse(resp, &payload); parseErr == nil {
					status["pods"] = len(objectItems(payload))
				}
			}
		}
	}
	RespondJSON(w, http.StatusOK, status)
}

func (h *MonitoringHandler) PreviewSharedAlertmanager(w http.ResponseWriter, r *http.Request) {
	req, values, backend, err := h.sharedAlertmanagerPayload(r.Context(), r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	replaceRequired, reasons := sharedAlertmanagerReplaceRequired(sharedAlertmanagerMetadata(backend), req)
	RespondJSON(w, http.StatusOK, map[string]any{
		"clusterId": req.ManagementClusterID,
		"chart": map[string]any{
			"repoUrl":   "https://prometheus-community.github.io/helm-charts",
			"chartName": "alertmanager",
		},
		"values":          sanitizeMonitoringValues(values),
		"desiredSpecHash": specHash(values),
		"requiresReplace": replaceRequired,
		"replaceReasons":  reasons,
	})
}

func (h *MonitoringHandler) InstallSharedAlertmanager(w http.ResponseWriter, r *http.Request) {
	req, values, backend, err := h.sharedAlertmanagerPayload(r.Context(), r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := h.updateSharedAlertmanagerMetadata(r.Context(), backend, req, "installing"); err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to persist shared Alertmanager metadata")
		return
	}
	op, err := h.enqueueSharedAlertmanagerOperation(r.Context(), currentUserUUID(r), "install", req, values)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to create monitoring operation")
		return
	}
	recordAudit(r, h.queries, "monitoring.shared_alertmanager.install", "monitoring_backend", backend.ID.String(), backend.BackendType, map[string]any{
		"managementClusterId": req.ManagementClusterID,
		"namespace":           req.Namespace,
		"releaseName":         req.ReleaseName,
		"operationId":         op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (h *MonitoringHandler) UpgradeSharedAlertmanager(w http.ResponseWriter, r *http.Request) {
	req, values, backend, err := h.sharedAlertmanagerPayload(r.Context(), r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if replaceRequired, reasons := sharedAlertmanagerReplaceRequired(sharedAlertmanagerMetadata(backend), req); replaceRequired {
		RespondJSON(w, http.StatusConflict, map[string]any{
			"error":           "replace_required",
			"message":         "Requested Alertmanager changes require reinstall rather than in-place upgrade",
			"requiresReplace": true,
			"replaceReasons":  reasons,
		})
		return
	}
	if err := h.updateSharedAlertmanagerMetadata(r.Context(), backend, req, "updating"); err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to persist shared Alertmanager metadata")
		return
	}
	op, err := h.enqueueSharedAlertmanagerOperation(r.Context(), currentUserUUID(r), "upgrade", req, values)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to create monitoring operation")
		return
	}
	recordAudit(r, h.queries, "monitoring.shared_alertmanager.upgrade", "monitoring_backend", backend.ID.String(), backend.BackendType, map[string]any{
		"managementClusterId": req.ManagementClusterID,
		"namespace":           req.Namespace,
		"releaseName":         req.ReleaseName,
		"operationId":         op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (h *MonitoringHandler) ReplaceSharedAlertmanager(w http.ResponseWriter, r *http.Request) {
	req, values, backend, err := h.sharedAlertmanagerPayload(r.Context(), r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	metadata := sharedAlertmanagerMetadata(backend)
	clusterID := defaultString(req.ManagementClusterID, stringFromMap(metadata, "managementClusterId"))
	namespace := defaultString(req.Namespace, defaultString(stringFromMap(metadata, "namespace"), "monitoring"))
	releaseName := defaultString(req.ReleaseName, defaultString(stringFromMap(metadata, "releaseName"), "astronomer-alertmanager"))

	if clusterID == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "managementClusterId is required")
		return
	}

	if err := h.updateSharedAlertmanagerMetadata(r.Context(), backend, req, "reinstalled"); err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to persist shared Alertmanager metadata")
		return
	}
	op, err := h.enqueueSharedAlertmanagerOperation(r.Context(), currentUserUUID(r), "replace", SharedAlertmanagerRequest{
		ManagementClusterID: clusterID,
		Namespace:           namespace,
		ReleaseName:         releaseName,
		ChartVersion:        req.ChartVersion,
		Replicas:            req.Replicas,
		StorageClass:        req.StorageClass,
		StorageSize:         req.StorageSize,
	}, values)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to create monitoring operation")
		return
	}
	recordAudit(r, h.queries, "monitoring.shared_alertmanager.replace", "monitoring_backend", backend.ID.String(), backend.BackendType, map[string]any{
		"managementClusterId": clusterID,
		"namespace":           namespace,
		"releaseName":         releaseName,
		"operationId":         op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (h *MonitoringHandler) UninstallSharedAlertmanager(w http.ResponseWriter, r *http.Request) {
	if h.helm == nil || h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "helm_error", "monitoring deployment is not configured")
		return
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil {
		RespondError(w, http.StatusBadRequest, "monitoring_error", "Default monitoring backend is not configured")
		return
	}
	metadata := sharedAlertmanagerMetadata(backend)
	clusterID := r.URL.Query().Get("clusterId")
	if clusterID == "" {
		clusterID = stringFromMap(metadata, "managementClusterId")
	}
	if clusterID == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "clusterId is required")
		return
	}
	namespace := defaultString(stringFromMap(metadata, "namespace"), "monitoring")
	releaseName := defaultString(stringFromMap(metadata, "releaseName"), "astronomer-alertmanager")
	if err := h.updateSharedAlertmanagerMetadata(r.Context(), backend, SharedAlertmanagerRequest{
		ManagementClusterID: clusterID,
		Namespace:           namespace,
		ReleaseName:         releaseName,
	}, "uninstalled"); err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to persist shared Alertmanager metadata")
		return
	}
	op, err := h.enqueueSharedAlertmanagerOperation(r.Context(), currentUserUUID(r), "uninstall", SharedAlertmanagerRequest{
		ManagementClusterID: clusterID,
		Namespace:           namespace,
		ReleaseName:         releaseName,
	}, nil)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to create monitoring operation")
		return
	}
	recordAudit(r, h.queries, "monitoring.shared_alertmanager.uninstall", "monitoring_backend", backend.ID.String(), backend.BackendType, map[string]any{
		"managementClusterId": clusterID,
		"namespace":           namespace,
		"releaseName":         releaseName,
		"operationId":         op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(op))
}

func (h *MonitoringHandler) GetSharedAlertmanagerStatus(w http.ResponseWriter, r *http.Request) {
	if !h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbRead) {
		return
	}
	if h.queries == nil {
		RespondJSON(w, http.StatusOK, map[string]any{"status": "not_configured"})
		return
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil {
		RespondJSON(w, http.StatusOK, map[string]any{"status": "not_configured"})
		return
	}
	metadata := sharedAlertmanagerMetadata(backend)
	status := map[string]any{
		"status":              defaultString(stringFromMap(metadata, "status"), "not_configured"),
		"managementClusterId": stringFromMap(metadata, "managementClusterId"),
		"namespace":           stringFromMap(metadata, "namespace"),
		"releaseName":         stringFromMap(metadata, "releaseName"),
		"chartVersion":        stringFromMap(metadata, "chartVersion"),
		"replicas":            metadata["replicas"],
		"storageClass":        stringFromMap(metadata, "storageClass"),
		"storageSize":         stringFromMap(metadata, "storageSize"),
		"desiredSpecHash":     stringFromMap(metadata, "lastAppliedSpecHash"),
		"managedAssetHashes":  mapFromMapValue(metadata["managedAssetHashes"]),
		"alertingAssetHashes": mapFromMapValue(mapFromMapValue(decodeJSONMap(backend.AuthConfig)["sharedAlertingAssets"])["hashes"]),
	}
	if observed, drifted, reasons := h.observeRelease(r.Context(), stringFromMap(metadata, "managementClusterId"), releaseRef{
		Namespace:   defaultString(stringFromMap(metadata, "namespace"), "monitoring"),
		ReleaseName: defaultString(stringFromMap(metadata, "releaseName"), "astronomer-alertmanager"),
	}); observed != nil {
		status["observedRelease"] = observed
		status["drifted"] = drifted
		status["driftReasons"] = reasons
		if drifted && status["status"] == "healthy" {
			status["status"] = "drifted"
		}
	}
	if op, ok := h.latestMonitoringOperation(r.Context(), "shared_alertmanager", "shared"); ok {
		status["operation"] = op
	}
	if h.requester != nil {
		clusterID := stringFromMap(metadata, "managementClusterId")
		namespace := defaultString(stringFromMap(metadata, "namespace"), "monitoring")
		releaseName := defaultString(stringFromMap(metadata, "releaseName"), "astronomer-alertmanager")
		if clusterID != "" {
			path := fmt.Sprintf("/api/v1/namespaces/%s/pods?labelSelector=%s", namespace, url.QueryEscape("app.kubernetes.io/instance="+releaseName))
			resp, doErr := h.requester.Do(r.Context(), clusterID, http.MethodGet, path, nil, requestHeaders(""))
			if doErr == nil && ensureSuccess(resp) == nil {
				var payload map[string]any
				if parseErr := parseJSONResponse(resp, &payload); parseErr == nil {
					status["pods"] = len(objectItems(payload))
				}
			}
		}
	}
	RespondJSON(w, http.StatusOK, status)
}

func (h *MonitoringHandler) GetClusterConfig(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondJSON(w, http.StatusOK, map[string]any{})
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	cfg, err := h.queries.GetClusterMonitoringConfig(r.Context(), clusterID)
	if err != nil {
		if err == pgx.ErrNoRows {
			RespondJSON(w, http.StatusOK, map[string]any{})
			return
		}
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to load cluster monitoring config")
		return
	}
	RespondJSON(w, http.StatusOK, clusterMonitoringConfigResponse(cfg))
}

func (h *MonitoringHandler) UpdateClusterConfig(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "monitoring_error", "monitoring store not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	var req UpdateClusterMonitoringConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	backendID := uuid.Nil
	if req.BackendID != nil {
		backendID = *req.BackendID
	} else {
		backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
		if err != nil {
			RespondError(w, http.StatusBadRequest, "monitoring_error", "Default monitoring backend is not configured")
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
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to save cluster monitoring config")
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
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
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
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := h.persistStackConfig(r.Context(), clusterID, req, "installing"); err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to persist monitoring stack config")
		return
	}
	op, err := h.enqueueClusterStackOperation(r.Context(), currentUserUUID(r), "install", clusterID, req, values)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to create monitoring operation")
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
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	cfg, ok, loadErr := h.loadStackConfig(r.Context(), clusterID)
	if loadErr != nil {
		RespondError(w, http.StatusBadRequest, "invalid_request", loadErr.Error())
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
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to persist monitoring stack config")
		return
	}
	op, err := h.enqueueClusterStackOperation(r.Context(), currentUserUUID(r), "upgrade", clusterID, req, values)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to create monitoring operation")
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
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	_, ok, loadErr := h.loadStackConfig(r.Context(), clusterID)
	if loadErr != nil {
		RespondError(w, http.StatusBadRequest, "invalid_request", loadErr.Error())
		return
	}
	if err := h.persistStackConfig(r.Context(), clusterID, req, "reinstalled"); err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to persist monitoring stack config")
		return
	}
	_ = ok
	op, err := h.enqueueClusterStackOperation(r.Context(), currentUserUUID(r), "replace", clusterID, req, values)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to create monitoring operation")
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
		RespondError(w, http.StatusServiceUnavailable, "helm_error", "helm requester not configured")
		return
	}
	clusterID := chi.URLParam(r, "cluster_id")
	cfg, _, err := h.loadStackConfig(r.Context(), clusterID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
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
	op, err := h.enqueueClusterStackOperation(r.Context(), currentUserUUID(r), "uninstall", clusterID, MonitoringStackRequest{
		ReleaseName: cfg.PrometheusReleaseName,
		Namespace:   cfg.StackNamespace,
	}, nil)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "monitoring_error", "Failed to create monitoring operation")
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
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
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

func (h *MonitoringHandler) clusterSummary(ctx context.Context, clusterID string) (map[string]any, error) {
	wh := NewWorkloadHandlerWithRequester(h.requester)
	nodes, err := wh.getNodes(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	pods, err := wh.listPods(ctx, clusterID, "", "")
	if err != nil {
		return nil, err
	}
	cpuCapacity := 0
	memoryCapacity := 0
	podCapacity := 0
	for _, node := range nodes {
		cpuCapacity += node["cpuCapacity"].(int)
		memoryCapacity += node["memoryCapacity"].(int)
		podCapacity += node["podCapacity"].(int)
	}
	cpuUsage := 0.0
	memoryUsage := 0.0
	if h.queries != nil {
		if id, err := uuid.Parse(clusterID); err == nil {
			if health, err := h.queries.GetClusterHealthStatus(ctx, id); err == nil {
				cpuUsage = float64(cpuCapacity) * (health.CpuUsagePercent / 100.0)
				memoryUsage = float64(memoryCapacity) * (health.MemoryUsagePercent / 100.0)
			}
		}
	}
	cpuPct := 0.0
	if cpuCapacity > 0 {
		cpuPct = (cpuUsage / float64(cpuCapacity)) * 100
	}
	memoryPct := 0.0
	if memoryCapacity > 0 {
		memoryPct = (memoryUsage / float64(memoryCapacity)) * 100
	}
	return map[string]any{
		"cpuUsage":         cpuUsage,
		"cpuCapacity":      cpuCapacity,
		"cpuPercentage":    cpuPct,
		"memoryUsage":      memoryUsage,
		"memoryCapacity":   memoryCapacity,
		"memoryPercentage": memoryPct,
		"podCount":         len(pods),
		"podCapacity":      podCapacity,
		"nodeCount":        len(nodes),
		"networkReceive":   0,
		"networkTransmit":  0,
		"diskUsage":        0,
		"diskCapacity":     0,
	}, nil
}

func (h *MonitoringHandler) zeroMetrics() map[string]any {
	series := func(name, unit string) map[string]any {
		return map[string]any{"name": name, "unit": unit, "data": []map[string]any{}}
	}
	return map[string]any{
		"cpuUsage":        series("CPU Usage", "cores"),
		"cpuCapacity":     series("CPU Capacity", "cores"),
		"memoryUsage":     series("Memory Usage", "bytes"),
		"memoryCapacity":  series("Memory Capacity", "bytes"),
		"networkReceive":  series("Network Receive", "bytes"),
		"networkTransmit": series("Network Transmit", "bytes"),
		"diskUsage":       series("Disk Usage", "bytes"),
		"podCount":        series("Pod Count", "count"),
	}
}

func (h *MonitoringHandler) workloadSummary(ctx context.Context, clusterID, kind, namespace, name string) (map[string]any, error) {
	clusterSummary, err := h.clusterSummary(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	wh := NewWorkloadHandlerWithRequester(h.requester)
	resource, err := wh.fetchWorkloadResource(ctx, clusterID, kind, namespace, name)
	if err != nil {
		return nil, err
	}
	workloadPods, err := wh.listPods(ctx, clusterID, namespace, labelSelector(resource.Spec.Selector.MatchLabels))
	if err != nil {
		return nil, err
	}
	allPods, err := wh.listPods(ctx, clusterID, "", "")
	if err != nil {
		return nil, err
	}
	podShare := 0.02
	if len(allPods) > 0 {
		podShare = float64(len(workloadPods)) / float64(len(allPods))
	}
	if podShare > 1 {
		podShare = 1
	}
	summary := cloneMetricSummary(clusterSummary)
	summary["cpuUsage"] = summaryFloat(clusterSummary["cpuUsage"]) * podShare
	summary["memoryUsage"] = summaryFloat(clusterSummary["memoryUsage"]) * podShare
	summary["networkReceive"] = summaryFloat(clusterSummary["networkReceive"]) * podShare
	summary["networkTransmit"] = summaryFloat(clusterSummary["networkTransmit"]) * podShare
	summary["diskUsage"] = summaryFloat(clusterSummary["diskUsage"]) * podShare
	summary["podCount"] = len(workloadPods)
	summary["podCapacity"] = len(workloadPods)
	return summary, nil
}

func (h *MonitoringHandler) metricsSeries(summary map[string]any, rawRange, label string) map[string]any {
	data := h.zeroMetrics()
	pointCount, span := metricWindow(rawRange)
	now := time.Now().UTC()
	series := func(name, unit string, current, baseline float64) map[string]any {
		return map[string]any{
			"name":  name,
			"label": label,
			"unit":  unit,
			"data":  metricPoints(now, pointCount, span, current, baseline),
		}
	}
	data["cpuUsage"] = series("CPU Usage", "cores", summaryFloat(summary["cpuUsage"]), 0.72)
	data["cpuCapacity"] = series("CPU Capacity", "cores", summaryFloat(summary["cpuCapacity"]), 1.0)
	data["memoryUsage"] = series("Memory Usage", "bytes", summaryFloat(summary["memoryUsage"]), 0.76)
	data["memoryCapacity"] = series("Memory Capacity", "bytes", summaryFloat(summary["memoryCapacity"]), 1.0)
	data["networkReceive"] = series("Network Receive", "bytes", summaryFloat(summary["networkReceive"]), 0.68)
	data["networkTransmit"] = series("Network Transmit", "bytes", summaryFloat(summary["networkTransmit"]), 0.64)
	data["diskUsage"] = series("Disk Usage", "bytes", summaryFloat(summary["diskUsage"]), 0.83)
	data["podCount"] = series("Pod Count", "count", summaryFloat(summary["podCount"]), 0.9)
	return data
}

func metricWindow(rawRange string) (int, time.Duration) {
	switch rawRange {
	case "6h":
		return 18, 6 * time.Hour
	case "24h":
		return 24, 24 * time.Hour
	case "7d":
		return 28, 7 * 24 * time.Hour
	default:
		return 12, time.Hour
	}
}

func metricPoints(now time.Time, count int, span time.Duration, current, baseline float64) []map[string]any {
	if count < 2 {
		count = 2
	}
	if baseline <= 0 {
		baseline = 1
	}
	step := span / time.Duration(count-1)
	points := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		ratio := baseline + (float64(i)/float64(count-1))*(1-baseline)
		points = append(points, map[string]any{
			"timestamp": now.Add(-span + (step * time.Duration(i))).Format(time.RFC3339),
			"value":     roundedMetric(current * ratio),
		})
	}
	return points
}

func roundedMetric(v float64) float64 {
	out, _ := strconv.ParseFloat(fmt.Sprintf("%.2f", v), 64)
	return out
}

func cloneMetricSummary(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func summaryFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func (h *MonitoringHandler) realClusterSummary(ctx context.Context, clusterID string) (map[string]any, bool, error) {
	client, cfg, ok, err := h.backendClient(ctx, clusterID)
	if err != nil || !ok {
		return nil, ok, err
	}
	selector := labelSelectorForConfig(cfg)
	cpuUsage, err := client.QueryScalar(ctx, `sum(rate(node_cpu_seconds_total{mode!="idle",`+selector+`}[5m]))`)
	if err != nil {
		return nil, true, err
	}
	cpuCapacity, err := client.QueryScalar(ctx, `sum(machine_cpu_cores{`+selector+`})`)
	if err != nil {
		return nil, true, err
	}
	memoryUsage, err := client.QueryScalar(ctx, `sum(node_memory_MemTotal_bytes{`+selector+`} - node_memory_MemAvailable_bytes{`+selector+`})`)
	if err != nil {
		return nil, true, err
	}
	memoryCapacity, err := client.QueryScalar(ctx, `sum(node_memory_MemTotal_bytes{`+selector+`})`)
	if err != nil {
		return nil, true, err
	}
	podCount, err := client.QueryScalar(ctx, `count(kube_pod_info{`+selector+`})`)
	if err != nil {
		return nil, true, err
	}
	podCapacity, err := client.QueryScalar(ctx, `sum(kube_node_status_capacity{resource="pods",unit="integer",`+selector+`})`)
	if err != nil {
		return nil, true, err
	}
	nodeCount, err := client.QueryScalar(ctx, `count(kube_node_info{`+selector+`})`)
	if err != nil {
		return nil, true, err
	}
	networkReceive, err := client.QueryScalar(ctx, `sum(rate(node_network_receive_bytes_total{device!~"lo|veth.*",`+selector+`}[5m]))`)
	if err != nil {
		return nil, true, err
	}
	networkTransmit, err := client.QueryScalar(ctx, `sum(rate(node_network_transmit_bytes_total{device!~"lo|veth.*",`+selector+`}[5m]))`)
	if err != nil {
		return nil, true, err
	}
	diskCapacity, err := client.QueryScalar(ctx, `sum(node_filesystem_size_bytes{mountpoint="/",fstype!~"tmpfs|overlay",`+selector+`})`)
	if err != nil {
		return nil, true, err
	}
	diskAvail, err := client.QueryScalar(ctx, `sum(node_filesystem_avail_bytes{mountpoint="/",fstype!~"tmpfs|overlay",`+selector+`})`)
	if err != nil {
		return nil, true, err
	}
	return metricSummary(cpuUsage, cpuCapacity, memoryUsage, memoryCapacity, podCount, podCapacity, nodeCount, networkReceive, networkTransmit, diskCapacity-diskAvail, diskCapacity), true, nil
}

func (h *MonitoringHandler) realClusterMetrics(ctx context.Context, clusterID, rawRange string) (map[string]any, bool, error) {
	client, cfg, ok, err := h.backendClient(ctx, clusterID)
	if err != nil || !ok {
		return nil, ok, err
	}
	selector := labelSelectorForConfig(cfg)
	points, span := metricWindow(rawRange)
	step := span / time.Duration(points-1)
	start := time.Now().UTC().Add(-span)
	end := time.Now().UTC()
	series, err := h.promSeriesSet(ctx, client, start, end, step, selector, "cluster", map[string]string{
		"cpuUsage":        `sum(rate(node_cpu_seconds_total{mode!="idle",%s}[5m]))`,
		"cpuCapacity":     `sum(machine_cpu_cores{%s})`,
		"memoryUsage":     `sum(node_memory_MemTotal_bytes{%s} - node_memory_MemAvailable_bytes{%s})`,
		"memoryCapacity":  `sum(node_memory_MemTotal_bytes{%s})`,
		"networkReceive":  `sum(rate(node_network_receive_bytes_total{device!~"lo|veth.*",%s}[5m]))`,
		"networkTransmit": `sum(rate(node_network_transmit_bytes_total{device!~"lo|veth.*",%s}[5m]))`,
		"diskUsage":       `sum(node_filesystem_size_bytes{mountpoint="/",fstype!~"tmpfs|overlay",%s} - node_filesystem_avail_bytes{mountpoint="/",fstype!~"tmpfs|overlay",%s})`,
		"podCount":        `count(kube_pod_info{%s})`,
	})
	if err != nil {
		return nil, true, err
	}
	return series, true, nil
}

func (h *MonitoringHandler) realWorkloadMetrics(ctx context.Context, clusterID, kind, namespace, name, rawRange string) (map[string]any, bool, error) {
	client, cfg, ok, err := h.backendClient(ctx, clusterID)
	if err != nil || !ok {
		return nil, ok, err
	}
	if h.requester == nil {
		return nil, false, nil
	}
	wh := NewWorkloadHandlerWithRequester(h.requester)
	resource, err := wh.fetchWorkloadResource(ctx, clusterID, kind, namespace, name)
	if err != nil {
		return nil, false, err
	}
	workloadPods, err := wh.listPods(ctx, clusterID, namespace, labelSelector(resource.Spec.Selector.MatchLabels))
	if err != nil {
		return nil, false, err
	}
	regex := podRegex(workloadPods)
	if regex == "" {
		return h.zeroMetrics(), true, nil
	}
	clusterSelector := labelSelectorForConfig(cfg)
	workloadSelector := `namespace="` + escapePromLabel(namespace) + `",pod=~"` + regex + `",` + clusterSelector
	points, span := metricWindow(rawRange)
	step := span / time.Duration(points-1)
	start := time.Now().UTC().Add(-span)
	end := time.Now().UTC()
	data := h.zeroMetrics()
	cpuUsage, err := client.QueryRange(ctx, fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{container!="",container!="POD",%s}[5m]))`, workloadSelector), start, end, step)
	if err != nil {
		return nil, true, err
	}
	cpuCapacity, err := client.QueryRange(ctx, fmt.Sprintf(`sum(kube_pod_container_resource_limits{resource="cpu",unit="core",%s})`, workloadSelector), start, end, step)
	if err != nil {
		return nil, true, err
	}
	memoryUsage, err := client.QueryRange(ctx, fmt.Sprintf(`sum(container_memory_working_set_bytes{container!="",container!="POD",%s})`, workloadSelector), start, end, step)
	if err != nil {
		return nil, true, err
	}
	memoryCapacity, err := client.QueryRange(ctx, fmt.Sprintf(`sum(kube_pod_container_resource_limits{resource="memory",unit="byte",%s})`, workloadSelector), start, end, step)
	if err != nil {
		return nil, true, err
	}
	data["cpuUsage"] = rangeSeries("CPU Usage", namespace+"/"+name, "cores", cpuUsage)
	data["cpuCapacity"] = rangeSeries("CPU Capacity", namespace+"/"+name, "cores", cpuCapacity)
	data["memoryUsage"] = rangeSeries("Memory Usage", namespace+"/"+name, "bytes", memoryUsage)
	data["memoryCapacity"] = rangeSeries("Memory Capacity", namespace+"/"+name, "bytes", memoryCapacity)
	data["podCount"] = rangeSeries("Pod Count", namespace+"/"+name, "count", constantPoints(end, span, points, float64(len(workloadPods))))
	return data, true, nil
}

func (h *MonitoringHandler) backendClient(ctx context.Context, clusterID string) (*imonitoring.Client, monitoringContext, bool, error) {
	if h.queries == nil {
		return nil, monitoringContext{}, false, nil
	}
	clusterUUID, err := uuid.Parse(clusterID)
	if err != nil {
		return nil, monitoringContext{}, false, err
	}
	if joined, err := h.queries.GetClusterMonitoringContext(ctx, clusterUUID); err == nil {
		client, err := imonitoring.NewClient(imonitoring.BackendConfig{
			QueryURL:           joined.QueryUrl,
			TenantID:           joined.TenantID,
			AuthType:           joined.AuthType,
			AuthConfig:         joined.AuthConfig,
			DefaultStepSeconds: joined.DefaultStepSeconds,
			TimeoutSeconds:     joined.TimeoutSeconds,
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
		QueryURL:           backend.QueryUrl,
		TenantID:           backend.TenantID,
		AuthType:           backend.AuthType,
		AuthConfig:         backend.AuthConfig,
		DefaultStepSeconds: backend.DefaultStepSeconds,
		TimeoutSeconds:     backend.TimeoutSeconds,
	})
	if err != nil {
		return nil, monitoringContext{}, false, err
	}
	return client, monitoringContext{ClusterLabel: "cluster_id", ClusterLabelValue: clusterID, DefaultStep: backend.DefaultStepSeconds}, true, nil
}

type monitoringContext struct {
	ClusterLabel      string
	ClusterLabelValue string
	DefaultStep       int32
}

func labelSelectorForConfig(cfg monitoringContext) string {
	return cfg.ClusterLabel + `="` + escapePromLabel(cfg.ClusterLabelValue) + `"`
}

func escapePromLabel(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return replacer.Replace(value)
}

func podRegex(items []map[string]any) string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		if name, ok := item["name"].(string); ok && name != "" {
			names = append(names, regexpEscape(name))
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "^(" + strings.Join(names, "|") + ")$"
}

func regexpEscape(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `.`, `\.`, `+`, `\+`, `*`, `\*`, `?`, `\?`, `(`, `\(`, `)`, `\)`, `[`, `\[`, `]`, `\]`, `{`, `\{`, `}`, `\}`, `^`, `\^`, `$`, `\$`, `|`, `\|`, `-`, `\-`)
	return replacer.Replace(value)
}

func (h *MonitoringHandler) promSeriesSet(ctx context.Context, client *imonitoring.Client, start, end time.Time, step time.Duration, selector, label string, queries map[string]string) (map[string]any, error) {
	data := h.zeroMetrics()
	for key, queryFmt := range queries {
		query := fmt.Sprintf(queryFmt, selector, selector)
		points, err := client.QueryRange(ctx, query, start, end, step)
		if err != nil {
			return nil, err
		}
		switch key {
		case "cpuUsage":
			data[key] = rangeSeries("CPU Usage", label, "cores", points)
		case "cpuCapacity":
			data[key] = rangeSeries("CPU Capacity", label, "cores", points)
		case "memoryUsage":
			data[key] = rangeSeries("Memory Usage", label, "bytes", points)
		case "memoryCapacity":
			data[key] = rangeSeries("Memory Capacity", label, "bytes", points)
		case "networkReceive":
			data[key] = rangeSeries("Network Receive", label, "bytes", points)
		case "networkTransmit":
			data[key] = rangeSeries("Network Transmit", label, "bytes", points)
		case "diskUsage":
			data[key] = rangeSeries("Disk Usage", label, "bytes", points)
		case "podCount":
			data[key] = rangeSeries("Pod Count", label, "count", points)
		}
	}
	return data, nil
}

func metricSummary(cpuUsage, cpuCapacity, memoryUsage, memoryCapacity, podCount, podCapacity, nodeCount, networkReceive, networkTransmit, diskUsage, diskCapacity float64) map[string]any {
	cpuPct := 0.0
	if cpuCapacity > 0 {
		cpuPct = (cpuUsage / cpuCapacity) * 100
	}
	memoryPct := 0.0
	if memoryCapacity > 0 {
		memoryPct = (memoryUsage / memoryCapacity) * 100
	}
	return map[string]any{
		"cpuUsage":         cpuUsage,
		"cpuCapacity":      cpuCapacity,
		"cpuPercentage":    cpuPct,
		"memoryUsage":      memoryUsage,
		"memoryCapacity":   memoryCapacity,
		"memoryPercentage": memoryPct,
		"podCount":         int(podCount),
		"podCapacity":      int(podCapacity),
		"nodeCount":        int(nodeCount),
		"networkReceive":   networkReceive,
		"networkTransmit":  networkTransmit,
		"diskUsage":        diskUsage,
		"diskCapacity":     diskCapacity,
	}
}

func rangeSeries(name, label, unit string, points []imonitoring.TimeSeriesPoint) map[string]any {
	items := make([]map[string]any, 0, len(points))
	for _, point := range points {
		items = append(items, map[string]any{"timestamp": point.Timestamp, "value": point.Value})
	}
	return map[string]any{"name": name, "label": label, "unit": unit, "data": items}
}

func constantPoints(now time.Time, span time.Duration, count int, value float64) []imonitoring.TimeSeriesPoint {
	if count < 2 {
		count = 2
	}
	step := span / time.Duration(count-1)
	out := make([]imonitoring.TimeSeriesPoint, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, imonitoring.TimeSeriesPoint{
			Timestamp: now.Add(-span + step*time.Duration(i)).UTC().Format(time.RFC3339),
			Value:     value,
		})
	}
	return out
}

func monitoringBackendResponse(backend sqlc.MonitoringBackend) map[string]any {
	authConfig := decodeJSONMap(backend.AuthConfig)
	return map[string]any{
		"id":                 backend.ID.String(),
		"name":               backend.Name,
		"backendType":        backend.BackendType,
		"queryUrl":           backend.QueryUrl,
		"alertmanagerUrl":    backend.AlertmanagerUrl,
		"tenantId":           backend.TenantID,
		"authType":           backend.AuthType,
		"authConfig":         authConfig,
		"operationPolicies":  mapFromMapValue(authConfig["operationPolicies"]),
		"defaultStepSeconds": backend.DefaultStepSeconds,
		"timeoutSeconds":     backend.TimeoutSeconds,
		"isDefault":          backend.IsDefault,
	}
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
	secretSpec, err := h.objectStoreSecretSpec(ctx, req.StorageConfigID, req.ObjectStorageSecretName, req.ReleaseName+"-objstore")
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

func sharedThanosMetadata(backend sqlc.MonitoringBackend) map[string]any {
	authCfg := decodeJSONMap(backend.AuthConfig)
	raw, ok := authCfg["sharedThanos"]
	if !ok {
		return map[string]any{}
	}
	metadata, ok := raw.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return metadata
}

func sharedAlertmanagerMetadata(backend sqlc.MonitoringBackend) map[string]any {
	authCfg := decodeJSONMap(backend.AuthConfig)
	raw, ok := authCfg["sharedAlertmanager"]
	if !ok {
		return map[string]any{}
	}
	metadata, ok := raw.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return metadata
}

func (h *MonitoringHandler) updateSharedThanosMetadata(ctx context.Context, backend sqlc.MonitoringBackend, req SharedThanosStackRequest, status string) error {
	if h.queries == nil {
		return nil
	}
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
	authCfg := decodeJSONMap(backend.AuthConfig)
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
	raw, err := json.Marshal(authCfg)
	if err != nil {
		return err
	}
	_, err = h.queries.UpsertDefaultMonitoringBackend(ctx, sqlc.UpsertDefaultMonitoringBackendParams{
		BackendType:        backend.BackendType,
		QueryUrl:           defaultSharedThanosQueryURL(backend.QueryUrl, req),
		AlertmanagerUrl:    backend.AlertmanagerUrl,
		TenantID:           backend.TenantID,
		AuthType:           backend.AuthType,
		AuthConfig:         raw,
		DefaultStepSeconds: backend.DefaultStepSeconds,
		TimeoutSeconds:     backend.TimeoutSeconds,
		CreatedByID:        backend.CreatedByID,
	})
	return err
}

func (h *MonitoringHandler) updateSharedAlertmanagerMetadata(ctx context.Context, backend sqlc.MonitoringBackend, req SharedAlertmanagerRequest, status string) error {
	if h.queries == nil {
		return nil
	}
	appliedSpecHash := specHash(map[string]any{
		"managementClusterId":   req.ManagementClusterID,
		"namespace":             defaultString(req.Namespace, "monitoring"),
		"releaseName":           defaultString(req.ReleaseName, "astronomer-alertmanager"),
		"chartVersion":          req.ChartVersion,
		"replicas":              req.Replicas,
		"storageClass":          req.StorageClass,
		"storageSize":           req.StorageSize,
		"autoRollbackOnFailure": boolPtrValue(req.AutoRollbackOnFailure),
	})
	authCfg := decodeJSONMap(backend.AuthConfig)
	authCfg["sharedAlertmanager"] = map[string]any{
		"managementClusterId": req.ManagementClusterID,
		"namespace":           defaultString(req.Namespace, "monitoring"),
		"releaseName":         defaultString(req.ReleaseName, "astronomer-alertmanager"),
		"status":              status,
		"chartVersion":        req.ChartVersion,
		"replicas":            req.Replicas,
		"storageClass":        req.StorageClass,
		"storageSize":         req.StorageSize,
		"lastAppliedSpecHash": appliedSpecHash,
		"updatedAt":           time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.Marshal(authCfg)
	if err != nil {
		return err
	}
	_, err = h.queries.UpsertDefaultMonitoringBackend(ctx, sqlc.UpsertDefaultMonitoringBackendParams{
		BackendType:        backend.BackendType,
		QueryUrl:           backend.QueryUrl,
		AlertmanagerUrl:    defaultSharedAlertmanagerURL(backend.AlertmanagerUrl, req),
		TenantID:           backend.TenantID,
		AuthType:           backend.AuthType,
		AuthConfig:         raw,
		DefaultStepSeconds: backend.DefaultStepSeconds,
		TimeoutSeconds:     backend.TimeoutSeconds,
		CreatedByID:        backend.CreatedByID,
	})
	return err
}

func (h *MonitoringHandler) sharedAlertmanagerPayload(ctx context.Context, r *http.Request) (SharedAlertmanagerRequest, map[string]any, sqlc.MonitoringBackend, error) {
	if h.queries == nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("monitoring store not configured")
	}
	if h.helm == nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("helm requester not configured")
	}

	var req SharedAlertmanagerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("invalid JSON body")
	}
	if req.ManagementClusterID == "" {
		req.ManagementClusterID = r.URL.Query().Get("clusterId")
	}
	if req.ManagementClusterID == "" {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("managementClusterId is required")
	}
	if req.Namespace == "" {
		req.Namespace = "monitoring"
	}
	if req.ReleaseName == "" {
		req.ReleaseName = "astronomer-alertmanager"
	}
	if req.ChartVersion == "" {
		req.ChartVersion = "1.18.0"
	}
	if req.Replicas <= 0 {
		req.Replicas = 1
	}
	if req.StorageSize == "" {
		req.StorageSize = "2Gi"
	}

	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("default monitoring backend is not configured")
	}
	channels, err := h.queries.ListNotificationChannels(ctx, sqlc.ListNotificationChannelsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, err
	}
	rules, err := h.queries.ListAlertRules(ctx, sqlc.ListAlertRulesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, err
	}
	routing, err := h.renderSharedAlertmanagerConfig(ctx, channels, rules)
	if err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, err
	}
	var config map[string]any
	if err := yaml.Unmarshal([]byte(routing), &config); err != nil {
		return SharedAlertmanagerRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("failed to parse alertmanager config")
	}

	persistence := map[string]any{"enabled": true, "size": req.StorageSize}
	if req.StorageClass != "" {
		persistence["storageClass"] = req.StorageClass
	}
	values := map[string]any{
		"replicaCount": req.Replicas,
		"persistence":  persistence,
		"config":       config,
		"configmapReload": map[string]any{
			"enabled": true,
		},
	}
	return req, values, backend, nil
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

func (h *MonitoringHandler) renderSharedAlertmanagerConfig(ctx context.Context, channels []sqlc.NotificationChannel, rules []sqlc.AlertRule) (string, error) {
	receivers := []map[string]any{{"name": "null"}}
	routes := []map[string]any{}
	for _, channel := range channels {
		if !channel.Enabled {
			continue
		}
		receiverName := "channel-" + channel.ID.String()
		receiver := map[string]any{"name": receiverName}
		cfg := decodeJSONMap(channel.Configuration)
		switch strings.ToLower(channel.ChannelType) {
		case "slack", "webhook":
			if webhook, ok := firstConfigString(cfg, "url", "webhook_url"); ok {
				receiver["webhook_configs"] = []map[string]any{{"url": webhook, "send_resolved": true}}
			}
		case "email":
			if email, ok := firstConfigString(cfg, "email", "address"); ok {
				receiver["email_configs"] = []map[string]any{{"to": email, "send_resolved": true}}
			}
		default:
			continue
		}
		receivers = append(receivers, receiver)
		channelRules, err := h.rulesForChannel(ctx, rules, channel.ID)
		if err != nil {
			return "", err
		}
		for _, rule := range channelRules {
			routes = append(routes, map[string]any{
				"receiver": receiverName,
				"matchers": []string{fmt.Sprintf(`astronomer_rule_id="%s"`, rule.ID.String())},
				"continue": true,
			})
		}
	}
	payload := map[string]any{
		"global": map[string]any{
			"resolve_timeout": "5m",
		},
		"route": map[string]any{
			"receiver":        "null",
			"group_by":        []string{"alertname", "astronomer_rule_id", "cluster"},
			"group_wait":      "30s",
			"group_interval":  "5m",
			"repeat_interval": "3h",
			"routes":          routes,
		},
		"receivers": receivers,
	}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (h *MonitoringHandler) rulesForChannel(ctx context.Context, allRules []sqlc.AlertRule, channelID uuid.UUID) ([]sqlc.AlertRule, error) {
	matched := make([]sqlc.AlertRule, 0)
	for _, rule := range allRules {
		channels, err := h.queries.ListChannelsForAlertRule(ctx, rule.ID)
		if err != nil {
			return nil, err
		}
		for _, channel := range channels {
			if channel.ID == channelID {
				matched = append(matched, rule)
				break
			}
		}
	}
	return matched, nil
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
		if strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "access_key") || strings.Contains(lower, "secret_key") || strings.Contains(lower, "objstoreconfig") {
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

func defaultSharedThanosQueryURL(current string, req SharedThanosStackRequest) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return fmt.Sprintf("http://%s-query-frontend.%s.svc.cluster.local:9090", defaultString(req.ReleaseName, "thanos"), defaultString(req.Namespace, "monitoring"))
}

func defaultSharedAlertmanagerURL(current string, req SharedAlertmanagerRequest) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:9093", defaultString(req.ReleaseName, "astronomer-alertmanager"), defaultString(req.Namespace, "monitoring"))
}

func sharedAlertmanagerReplaceRequired(metadata map[string]any, req SharedAlertmanagerRequest) (bool, []string) {
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
	if current := stringFromMap(metadata, "storageClass"); current != req.StorageClass {
		reasons = append(reasons, "storage class change")
	}
	if current := stringFromMap(metadata, "storageSize"); current != "" && current != req.StorageSize {
		reasons = append(reasons, "storage size change")
	}
	return len(reasons) > 0, reasons
}

func sharedThanosReplaceRequired(metadata map[string]any, req SharedThanosStackRequest) (bool, []string) {
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
	return len(reasons) > 0, reasons
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

func (h *MonitoringHandler) objectStoreSecretSpec(ctx context.Context, storageConfigID, overrideName, defaultName string) (objectStoreSecretSpec, error) {
	if h.queries == nil {
		return objectStoreSecretSpec{}, fmt.Errorf("monitoring store not configured")
	}
	storageID, err := uuid.Parse(storageConfigID)
	if err != nil {
		return objectStoreSecretSpec{}, fmt.Errorf("invalid storageConfigId")
	}
	storageCfg, err := h.queries.GetBackupStorageConfigByID(ctx, storageID)
	if err != nil {
		return objectStoreSecretSpec{}, fmt.Errorf("backup storage config not found")
	}
	if storageCfg.Bucket == "" {
		return objectStoreSecretSpec{}, fmt.Errorf("storage config bucket is required")
	}
	content, err := buildObjstoreConfigYAML(storageCfg)
	if err != nil {
		return objectStoreSecretSpec{}, fmt.Errorf("failed to build object storage config")
	}
	name := defaultString(overrideName, defaultName)
	return objectStoreSecretSpec{
		Name:            name,
		Key:             "objstore.yml",
		Content:         content,
		StorageConfigID: storageConfigID,
	}, nil
}

func buildObjstoreConfigYAML(storageCfg sqlc.BackupStorageConfig) (string, error) {
	objstoreConfig := map[string]any{
		"type": "S3",
		"config": map[string]any{
			"bucket":     storageCfg.Bucket,
			"endpoint":   storageCfg.EndpointUrl,
			"region":     storageCfg.Region,
			"access_key": storageCfg.AccessKey,
			"secret_key": storageCfg.SecretKey,
		},
	}
	if storageCfg.Prefix != "" {
		objstoreConfig["prefix"] = storageCfg.Prefix
	}
	if storageCfg.EndpointUrl == "" {
		objstoreConfig["config"].(map[string]any)["endpoint"] = "s3.amazonaws.com"
	}
	raw, err := yaml.Marshal(objstoreConfig)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (h *MonitoringHandler) ensureObjectStoreSecret(ctx context.Context, clusterID, namespace string, spec objectStoreSecretSpec) error {
	if h.requester == nil {
		return fmt.Errorf("kubernetes requester not configured")
	}
	if err := h.ensureNamespace(ctx, clusterID, namespace); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      spec.Name,
			"namespace": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
				"astronomer.io/component":      "monitoring",
			},
		},
		"type": "Opaque",
		"stringData": map[string]string{
			spec.Key: spec.Content,
		},
	})
	if err != nil {
		return err
	}
	patchPath := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, spec.Name)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodPatch, patchPath, body, requestHeaders("application/merge-patch+json"))
	if err == nil && resp != nil && resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	createPath := fmt.Sprintf("/api/v1/namespaces/%s/secrets", namespace)
	resp, err = h.requester.Do(ctx, clusterID, http.MethodPost, createPath, body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	return ensureSuccess(resp)
}

func (h *MonitoringHandler) ensureNamespace(ctx context.Context, clusterID, namespace string) error {
	if h.requester == nil {
		return fmt.Errorf("kubernetes requester not configured")
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s", namespace)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusBadRequest {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
			},
		},
	})
	if err != nil {
		return err
	}
	resp, err = h.requester.Do(ctx, clusterID, http.MethodPost, "/api/v1/namespaces", body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	return ensureSuccess(resp)
}

func (h *MonitoringHandler) applyMonitoringStack(ctx context.Context, clusterID string, msgType protocol.MessageType, req MonitoringStackRequest, values map[string]any) (*protocol.HelmResultPayload, error) {
	if h.helm == nil {
		return nil, fmt.Errorf("helm requester not configured")
	}
	if req.StorageConfigID != "" {
		secretSpec, err := h.objectStoreSecretSpec(ctx, req.StorageConfigID, req.ObjectStorageSecretName, req.ReleaseName+"-thanos-objstore")
		if err != nil {
			return nil, err
		}
		if err := h.ensureObjectStoreSecret(ctx, clusterID, req.Namespace, secretSpec); err != nil {
			return nil, err
		}
	}
	return h.helm.Do(ctx, clusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: req.ReleaseName,
		Namespace:   req.Namespace,
		ChartName:   "kube-prometheus-stack",
		RepoURL:     "https://prometheus-community.github.io/helm-charts",
		Version:     req.ChartVersion,
		Values:      values,
		Timeout:     900,
	})
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

func scrapeIntervalSeconds(raw string) int32 {
	if raw == "" {
		return 30
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 30
	}
	return int32(d.Seconds())
}

func defaultInt32(v, fallback int32) int32 {
	if v <= 0 {
		return fallback
	}
	return v
}

func nullableNow(ok bool) pgtype.Timestamptz {
	if !ok {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
}

func specHash(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func mapFromMapValue(v any) map[string]any {
	out, _ := v.(map[string]any)
	if out == nil {
		return map[string]any{}
	}
	return out
}

func (h *MonitoringHandler) observeRelease(ctx context.Context, clusterID string, ref releaseRef) (map[string]any, bool, []string) {
	if h.helm == nil || clusterID == "" || ref.ReleaseName == "" || ref.Namespace == "" {
		return nil, false, nil
	}
	result, err := h.helm.Status(ctx, clusterID, ref.ReleaseName, ref.Namespace)
	observed := map[string]any{
		"clusterId":   clusterID,
		"namespace":   ref.Namespace,
		"releaseName": ref.ReleaseName,
		"observedAt":  time.Now().UTC().Format(time.RFC3339),
	}
	if err != nil {
		observed["status"] = "missing"
		observed["error"] = err.Error()
		return observed, true, []string{"helm release not found or not healthy"}
	}
	observed["status"] = result.Status
	observed["revision"] = result.Revision
	return observed, false, nil
}

func (h *MonitoringHandler) enqueueSharedThanosOperation(ctx context.Context, userID pgtype.UUID, opType string, req SharedThanosStackRequest, values map[string]any, secretSpec *objectStoreSecretSpec) (sqlc.MonitoringOperation, error) {
	rawReq, err := json.Marshal(req)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	payload, err := json.Marshal(monitoringOperationEnvelope{
		ClusterID:                req.ManagementClusterID,
		Request:                  rawReq,
		Values:                   values,
		SecretSpec:               secretSpec,
		ResolvedAutoRollback:     h.resolveAutoRollbackPolicy(backend, req.AutoRollbackOnFailure),
		ResolvedMaxRetryAttempts: h.resolveMaxRetryAttempts(backend),
	})
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	op, err := h.queries.CreateMonitoringOperation(ctx, sqlc.CreateMonitoringOperationParams{
		TargetType:    "shared_thanos",
		TargetKey:     "shared",
		OperationType: opType,
		Payload:       payload,
		Status:        "pending",
		CreatedByID:   userID,
	})
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

func (h *MonitoringHandler) enqueueSharedAlertmanagerOperation(ctx context.Context, userID pgtype.UUID, opType string, req SharedAlertmanagerRequest, values map[string]any) (sqlc.MonitoringOperation, error) {
	rawReq, err := json.Marshal(req)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	payload, err := json.Marshal(monitoringOperationEnvelope{
		ClusterID:                req.ManagementClusterID,
		Request:                  rawReq,
		Values:                   values,
		ResolvedAutoRollback:     h.resolveAutoRollbackPolicy(backend, req.AutoRollbackOnFailure),
		ResolvedMaxRetryAttempts: h.resolveMaxRetryAttempts(backend),
	})
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	op, err := h.queries.CreateMonitoringOperation(ctx, sqlc.CreateMonitoringOperationParams{
		TargetType:    "shared_alertmanager",
		TargetKey:     "shared",
		OperationType: opType,
		Payload:       payload,
		Status:        "pending",
		CreatedByID:   userID,
	})
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

func (h *MonitoringHandler) enqueueClusterStackOperation(ctx context.Context, userID pgtype.UUID, opType, clusterID string, req MonitoringStackRequest, values map[string]any) (sqlc.MonitoringOperation, error) {
	rawReq, err := json.Marshal(req)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	payload, err := json.Marshal(monitoringOperationEnvelope{
		ClusterID:                clusterID,
		Request:                  rawReq,
		Values:                   values,
		ResolvedAutoRollback:     h.resolveAutoRollbackPolicy(backend, req.AutoRollbackOnFailure),
		ResolvedMaxRetryAttempts: h.resolveMaxRetryAttempts(backend),
	})
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	op, err := h.queries.CreateMonitoringOperation(ctx, sqlc.CreateMonitoringOperationParams{
		TargetType:    "cluster_stack",
		TargetKey:     clusterID,
		OperationType: opType,
		Payload:       payload,
		Status:        "pending",
		CreatedByID:   userID,
	})
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

func monitoringOperationResponse(op sqlc.MonitoringOperation) map[string]any {
	return map[string]any{
		"id":            op.ID.String(),
		"targetType":    op.TargetType,
		"targetKey":     op.TargetKey,
		"operationType": op.OperationType,
		"status":        op.Status,
		"attemptCount":  op.AttemptCount,
		"startedAt":     nullablePgTime(op.StartedAt),
		"completedAt":   nullablePgTime(op.CompletedAt),
		"errorMessage":  op.ErrorMessage,
		"createdAt":     op.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":     op.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func monitoringOperationEventsResponse(events []sqlc.MonitoringOperationEvent) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		out = append(out, map[string]any{
			"id":        event.ID.String(),
			"level":     event.Level,
			"stage":     event.Stage,
			"message":   event.Message,
			"detail":    decodeJSONMap(event.Detail),
			"createdAt": event.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func (h *MonitoringHandler) latestMonitoringOperation(ctx context.Context, targetType, targetKey string) (map[string]any, bool) {
	if h.queries == nil {
		return nil, false
	}
	op, err := h.queries.GetLatestMonitoringOperationForTarget(ctx, sqlc.GetLatestMonitoringOperationForTargetParams{
		TargetType: targetType,
		TargetKey:  targetKey,
	})
	if err != nil {
		return nil, false
	}
	return monitoringOperationResponse(op), true
}

func (h *MonitoringHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	if h == nil || h.queries == nil {
		return map[string]any{
			"reconciler": map[string]any{"enabled": false, "queueDepth": 0},
			"operations": map[string]int{},
		}, nil
	}
	ops, err := h.queries.ListMonitoringOperations(ctx, sqlc.ListMonitoringOperationsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	bindings, restricted, err := h.authz.bindingsForContext(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	staleRunning := 0
	recent := make([]map[string]any, 0, min(len(ops), 5))
	var latestFailure map[string]any
	recentFailureCount := 0
	for _, op := range ops {
		if restricted {
			allowed, err := h.canReadMonitoringOperation(ctx, bindings, op)
			if err != nil || !allowed {
				continue
			}
		}
		counts[op.Status]++
		if op.Status == "running" && op.StartedAt.Valid && time.Since(op.StartedAt.Time) > 2*time.Minute {
			staleRunning++
		}
		if len(recent) < 5 {
			recent = append(recent, h.monitoringOperationPreview(ctx, op))
		}
		if (op.Status == "failed" || op.Status == "superseded") && time.Since(op.CreatedAt) <= 30*time.Minute {
			recentFailureCount++
		}
		if latestFailure == nil && (op.Status == "failed" || op.Status == "superseded") {
			latestFailure = h.monitoringOperationPreview(ctx, op)
		}
	}
	summary := map[string]any{
		"reconciler": map[string]any{
			"enabled":              true,
			"queueDepth":           counts["pending"] + counts["running"],
			"staleRunningCount":    staleRunning,
			"staleThresholdSecond": 120,
		},
		"operations":         counts,
		"recentFailureCount": recentFailureCount,
		"recentOperations":   recent,
		"latestFailure":      latestFailure,
	}
	if backend, err := h.queries.GetDefaultMonitoringBackend(ctx); err == nil {
		metadata := decodeJSONMap(backend.AuthConfig)
		summary["backend"] = map[string]any{
			"type":     backend.BackendType,
			"queryUrl": backend.QueryUrl,
			"healthy":  strings.EqualFold(fmt.Sprint(metadata["status"]), "healthy"),
			"status":   firstNonEmptyString(fmt.Sprint(metadata["status"]), "unknown"),
		}
	}
	return summary, nil
}

func (h *MonitoringHandler) monitoringOperationPreview(ctx context.Context, op sqlc.MonitoringOperation) map[string]any {
	resp := monitoringOperationResponse(op)
	if events, err := h.queries.ListMonitoringOperationEvents(ctx, op.ID); err == nil && len(events) > 0 {
		resp["eventsPreview"] = monitoringOperationEventsResponse(lastMonitoringEvents(events, 3))
	}
	return resp
}

func (h *MonitoringHandler) authorizeMonitoringOperationRead(w http.ResponseWriter, r *http.Request, op sqlc.MonitoringOperation) bool {
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "permission_error", "Failed to retrieve user permissions")
		return false
	}
	if !restricted {
		return true
	}
	allowed, err := h.canReadMonitoringOperation(r.Context(), bindings, op)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "resolve_error", "Failed to resolve monitoring operation target")
		return false
	}
	if !allowed {
		RespondError(w, http.StatusForbidden, "permission_denied", "You do not have permission to access this operation")
		return false
	}
	return true
}

func (h *MonitoringHandler) canReadMonitoringOperation(ctx context.Context, bindings []rbac.RoleBinding, op sqlc.MonitoringOperation) (bool, error) {
	switch op.TargetType {
	case "shared_thanos", "shared_alertmanager":
		return h.authz.allowsGlobal(bindings, rbac.ResourceMonitoring, rbac.VerbRead), nil
	case "cluster_stack":
		clusterID, err := uuid.Parse(op.TargetKey)
		if err != nil {
			return false, err
		}
		return h.authz.allowsCluster(bindings, clusterID, rbac.ResourceMonitoring, rbac.VerbRead), nil
	default:
		return h.authz.allowsGlobal(bindings, rbac.ResourceMonitoring, rbac.VerbRead), nil
	}
}

func lastMonitoringEvents(events []sqlc.MonitoringOperationEvent, n int) []sqlc.MonitoringOperationEvent {
	if len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}

func (h *MonitoringHandler) processPendingMonitoringOperations(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListPendingMonitoringOperations(ctx, 20)
	if err != nil {
		if h.log != nil {
			h.log.Warn("failed to list pending monitoring operations", "error", err)
		}
		return
	}
	latestByTarget := make(map[string]uuid.UUID, len(ops))
	for i := len(ops) - 1; i >= 0; i-- {
		key := ops[i].TargetType + ":" + ops[i].TargetKey
		if _, ok := latestByTarget[key]; !ok {
			latestByTarget[key] = ops[i].ID
		}
	}
	for _, op := range ops {
		targetKey := op.TargetType + ":" + op.TargetKey
		if latestID, ok := latestByTarget[targetKey]; ok && latestID != op.ID {
			if op.Status == "pending" || (op.Status == "running" && (!op.StartedAt.Valid || time.Since(op.StartedAt.Time) >= 2*time.Minute)) {
				h.recordMonitoringOperationEvent(ctx, op.ID, "info", "queue", "operation superseded by newer desired state", map[string]any{
					"targetType": op.TargetType,
					"targetKey":  op.TargetKey,
				})
				_, _ = h.queries.MarkMonitoringOperationSuperseded(ctx, sqlc.MarkMonitoringOperationSupersededParams{
					ID:           op.ID,
					ErrorMessage: "superseded by newer operation for target",
				})
			}
			continue
		}
		if op.Status == "running" && op.StartedAt.Valid && time.Since(op.StartedAt.Time) < 2*time.Minute {
			continue
		}
		running, err := h.queries.MarkMonitoringOperationRunning(ctx, op.ID)
		if err != nil {
			continue
		}
		maxAttempts := h.operationMaxAttempts(running.Payload)
		h.recordMonitoringOperationEvent(ctx, running.ID, "info", "queue", "operation execution started", map[string]any{
			"operationType": running.OperationType,
			"targetType":    running.TargetType,
			"targetKey":     running.TargetKey,
			"attemptCount":  running.AttemptCount,
			"maxAttempts":   maxAttempts,
		})
		if err := h.executeMonitoringOperation(ctx, running); err != nil {
			h.recordMonitoringOperationEvent(ctx, running.ID, "error", "complete", "operation failed", map[string]any{
				"error": err.Error(),
			})
			_, _ = h.queries.MarkMonitoringOperationFailed(ctx, sqlc.MarkMonitoringOperationFailedParams{ID: running.ID, ErrorMessage: err.Error()})
			if running.AttemptCount < maxAttempts {
				h.recordMonitoringOperationEvent(ctx, running.ID, "warn", "retry", "operation requeued by retry policy", map[string]any{
					"attemptCount": running.AttemptCount,
					"maxAttempts":  maxAttempts,
				})
				_, _ = h.queries.RequeueMonitoringOperation(ctx, running.ID)
			}
			if h.log != nil {
				h.log.Warn("monitoring operation failed", "id", running.ID.String(), "target_type", running.TargetType, "operation_type", running.OperationType, "error", err)
			}
			continue
		}
		h.recordMonitoringOperationEvent(ctx, running.ID, "info", "complete", "operation completed", map[string]any{})
		_, _ = h.queries.MarkMonitoringOperationCompleted(ctx, running.ID)
	}
}

func (h *MonitoringHandler) executeMonitoringOperation(ctx context.Context, op sqlc.MonitoringOperation) error {
	var env monitoringOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return err
	}
	switch op.TargetType {
	case "shared_thanos":
		var req SharedThanosStackRequest
		if err := json.Unmarshal(env.Request, &req); err != nil {
			return err
		}
		switch op.OperationType {
		case "install":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Thanos install", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedThanosStack(ctx, protocol.MsgHelmInstall, req, valueOrZeroSecret(env.SecretSpec), env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return err
			}
			return h.verifySharedThanosReadiness(ctx, op.ID, req)
		case "upgrade":
			previousRevision := h.currentReleaseRevision(ctx, req.ManagementClusterID, req.ReleaseName, req.Namespace)
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Thanos upgrade", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedThanosStack(ctx, protocol.MsgHelmUpgrade, req, valueOrZeroSecret(env.SecretSpec), env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			if err := h.verifySharedThanosReadiness(ctx, op.ID, req); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			return nil
		case "replace":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling existing shared Thanos release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "install", "installing replacement shared Thanos release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err = h.applySharedThanosStack(ctx, protocol.MsgHelmInstall, req, valueOrZeroSecret(env.SecretSpec), env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return err
			}
			return h.verifySharedThanosReadiness(ctx, op.ID, req)
		case "uninstall":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling shared Thanos release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			return nil
		}
	case "shared_alertmanager":
		var req SharedAlertmanagerRequest
		if err := json.Unmarshal(env.Request, &req); err != nil {
			return err
		}
		switch op.OperationType {
		case "install":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Alertmanager install", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedAlertmanager(ctx, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return err
			}
			return h.verifySharedAlertmanagerReadiness(ctx, op.ID, req)
		case "upgrade":
			previousRevision := h.currentReleaseRevision(ctx, req.ManagementClusterID, req.ReleaseName, req.Namespace)
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Alertmanager upgrade", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedAlertmanager(ctx, protocol.MsgHelmUpgrade, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			if err := h.verifySharedAlertmanagerReadiness(ctx, op.ID, req); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			return nil
		case "replace":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling existing shared Alertmanager release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "install", "installing replacement shared Alertmanager release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err = h.applySharedAlertmanager(ctx, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return err
			}
			return h.verifySharedAlertmanagerReadiness(ctx, op.ID, req)
		case "uninstall":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling shared Alertmanager release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			return nil
		}
	case "cluster_stack":
		var req MonitoringStackRequest
		if err := json.Unmarshal(env.Request, &req); err != nil {
			return err
		}
		switch op.OperationType {
		case "install":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying cluster monitoring install", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applyMonitoringStack(ctx, env.ClusterID, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, env.ClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return err
			}
			return h.verifyClusterMonitoringReadiness(ctx, op.ID, env.ClusterID, req)
		case "upgrade":
			previousRevision := h.currentReleaseRevision(ctx, env.ClusterID, req.ReleaseName, req.Namespace)
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying cluster monitoring upgrade", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applyMonitoringStack(ctx, env.ClusterID, protocol.MsgHelmUpgrade, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, env.ClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, env.ClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			if err := h.verifyClusterMonitoringReadiness(ctx, op.ID, env.ClusterID, req); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, env.ClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			return nil
		case "replace":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling existing cluster monitoring release", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, env.ClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "install", "installing replacement cluster monitoring release", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err = h.applyMonitoringStack(ctx, env.ClusterID, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, env.ClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return err
			}
			return h.verifyClusterMonitoringReadiness(ctx, op.ID, env.ClusterID, req)
		case "uninstall":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling cluster monitoring release", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, env.ClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 600})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("unsupported monitoring operation: %s/%s", op.TargetType, op.OperationType)
}

func isReleaseMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "release: not found")
}

func valueOrZeroSecret(spec *objectStoreSecretSpec) objectStoreSecretSpec {
	if spec == nil {
		return objectStoreSecretSpec{}
	}
	return *spec
}

func (h *MonitoringHandler) resolveAutoRollbackPolicy(backend sqlc.MonitoringBackend, override *bool) bool {
	if override != nil {
		return *override
	}
	policies := mapFromMapValue(decodeJSONMap(backend.AuthConfig)["operationPolicies"])
	if value, ok := policies["defaultAutoRollbackOnFailure"].(bool); ok {
		return value
	}
	return false
}

func (h *MonitoringHandler) resolveMaxRetryAttempts(backend sqlc.MonitoringBackend) int32 {
	policies := mapFromMapValue(decodeJSONMap(backend.AuthConfig)["operationPolicies"])
	switch value := policies["maxRetryAttempts"].(type) {
	case float64:
		if value >= 1 {
			return int32(value)
		}
	case int32:
		if value >= 1 {
			return value
		}
	case int:
		if value >= 1 {
			return int32(value)
		}
	}
	return 1
}

func (h *MonitoringHandler) operationMaxAttempts(payload json.RawMessage) int32 {
	var env monitoringOperationEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return 1
	}
	if env.ResolvedMaxRetryAttempts < 1 {
		return 1
	}
	return env.ResolvedMaxRetryAttempts
}

func (h *MonitoringHandler) currentReleaseRevision(ctx context.Context, clusterID, releaseName, namespace string) int {
	if h == nil || h.helm == nil || clusterID == "" || releaseName == "" || namespace == "" {
		return 0
	}
	result, err := h.helm.Status(ctx, clusterID, releaseName, namespace)
	if err != nil {
		return 0
	}
	return result.Revision
}

func boolPtrValue(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func (h *MonitoringHandler) rollbackIfConfigured(ctx context.Context, operationID uuid.UUID, originalErr error, enabled bool, clusterID, releaseName, namespace string, previousRevision int) error {
	if !enabled {
		return originalErr
	}
	if previousRevision <= 0 {
		return fmt.Errorf("%w; rollback requested but no previous revision was available", originalErr)
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "warn", "rollback", "upgrade failed readiness, attempting rollback", map[string]any{
		"clusterId":        clusterID,
		"releaseName":      releaseName,
		"namespace":        namespace,
		"previousRevision": previousRevision,
		"error":            originalErr.Error(),
	})
	_, rollbackErr := h.helm.Do(ctx, clusterID, protocol.MsgHelmRollback, protocol.HelmRequestPayload{
		ReleaseName: releaseName,
		Namespace:   namespace,
		Revision:    previousRevision,
		Timeout:     900,
	})
	if rollbackErr != nil {
		h.recordMonitoringOperationEvent(ctx, operationID, "error", "rollback", "rollback failed", map[string]any{
			"clusterId":        clusterID,
			"releaseName":      releaseName,
			"namespace":        namespace,
			"previousRevision": previousRevision,
			"error":            rollbackErr.Error(),
		})
		return fmt.Errorf("%w; rollback to revision %d failed: %v", originalErr, previousRevision, rollbackErr)
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "rollback", "rollback completed", map[string]any{
		"clusterId":        clusterID,
		"releaseName":      releaseName,
		"namespace":        namespace,
		"previousRevision": previousRevision,
	})
	if err := h.waitForReleaseReadiness(ctx, operationID, clusterID, namespace, releaseName, 1, 2*time.Minute); err != nil {
		return fmt.Errorf("%w; rollback succeeded but readiness after rollback failed: %v", originalErr, err)
	}
	return fmt.Errorf("%w; rollback to revision %d completed", originalErr, previousRevision)
}

func (h *MonitoringHandler) recordMonitoringOperationEvent(ctx context.Context, operationID uuid.UUID, level, stage, message string, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, _ = h.queries.CreateMonitoringOperationEvent(ctx, sqlc.CreateMonitoringOperationEventParams{
		OperationID: operationID,
		Level:       level,
		Stage:       stage,
		Message:     message,
		Detail:      raw,
	})
}

func (h *MonitoringHandler) waitForReleaseReadiness(ctx context.Context, operationID uuid.UUID, clusterID, namespace, releaseName string, minReadyPods int, timeout time.Duration) error {
	if h.requester == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	labelSelector := url.QueryEscape("app.kubernetes.io/instance=" + releaseName)
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "readiness", "waiting for release readiness", map[string]any{
		"clusterId":    clusterID,
		"namespace":    namespace,
		"releaseName":  releaseName,
		"minReadyPods": minReadyPods,
		"timeout":      timeout.String(),
	})
	for {
		ready, total, err := h.countReadyReleasePods(ctx, clusterID, namespace, labelSelector)
		if err == nil && total >= minReadyPods && ready >= minReadyPods {
			h.recordMonitoringOperationEvent(ctx, operationID, "info", "readiness", "release became ready", map[string]any{
				"clusterId":   clusterID,
				"namespace":   namespace,
				"releaseName": releaseName,
				"readyPods":   ready,
				"totalPods":   total,
			})
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("release readiness check timed out: %w", err)
			}
			return fmt.Errorf("release readiness check timed out: %d/%d ready pods for %s", ready, total, releaseName)
		}
		if err != nil {
			h.recordMonitoringOperationEvent(ctx, operationID, "warn", "readiness", "release readiness poll failed", map[string]any{
				"error": err.Error(),
			})
		}
		time.Sleep(5 * time.Second)
	}
}

func (h *MonitoringHandler) countReadyReleasePods(ctx context.Context, clusterID, namespace, labelSelector string) (int, int, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods?labelSelector=%s", namespace, labelSelector)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return 0, 0, err
	}
	if err := ensureSuccess(resp); err != nil {
		return 0, 0, err
	}
	var payload map[string]any
	if err := parseJSONResponse(resp, &payload); err != nil {
		return 0, 0, err
	}
	items := objectItems(payload)
	ready := 0
	for _, item := range items {
		if podReady(item) {
			ready++
		}
	}
	return ready, len(items), nil
}

func podReady(item map[string]any) bool {
	status, _ := item["status"].(map[string]any)
	phase, _ := status["phase"].(string)
	if phase != "Running" {
		return false
	}
	conditions, _ := status["conditions"].([]any)
	for _, cond := range conditions {
		entry, _ := cond.(map[string]any)
		if entry == nil {
			continue
		}
		if entry["type"] == "Ready" && entry["status"] == "True" {
			return true
		}
	}
	return false
}

func (h *MonitoringHandler) verifySharedThanosReadiness(ctx context.Context, operationID uuid.UUID, req SharedThanosStackRequest) error {
	serviceName := defaultString(req.ReleaseName, "thanos") + "-query-frontend"
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "service", "checking shared Thanos query frontend health", map[string]any{
		"service":   serviceName,
		"namespace": req.Namespace,
	})
	if err := h.waitForServiceProxySuccess(ctx, req.ManagementClusterID, req.Namespace, serviceName, "9090", "/-/healthy", "", 90*time.Second); err != nil {
		return err
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "smoke", "running shared Thanos PromQL smoke query", map[string]any{
		"service": serviceName,
	})
	return h.waitForServiceProxySuccess(ctx, req.ManagementClusterID, req.Namespace, serviceName, "9090", "/api/v1/query", "query=vector(1)", 90*time.Second)
}

func (h *MonitoringHandler) verifySharedAlertmanagerReadiness(ctx context.Context, operationID uuid.UUID, req SharedAlertmanagerRequest) error {
	serviceName := defaultString(req.ReleaseName, "astronomer-alertmanager")
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "service", "checking shared Alertmanager health", map[string]any{
		"service":   serviceName,
		"namespace": req.Namespace,
	})
	return h.waitForServiceProxySuccess(ctx, req.ManagementClusterID, req.Namespace, serviceName, "9093", "/-/healthy", "", 90*time.Second)
}

func (h *MonitoringHandler) verifyClusterMonitoringReadiness(ctx context.Context, operationID uuid.UUID, clusterID string, req MonitoringStackRequest) error {
	serviceName, err := h.findPrometheusServiceName(ctx, clusterID, req.Namespace, req.ReleaseName)
	if err != nil {
		return err
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "service", "checking cluster Prometheus service health", map[string]any{
		"service":   serviceName,
		"namespace": req.Namespace,
	})
	if err := h.waitForServiceProxySuccess(ctx, clusterID, req.Namespace, serviceName, "9090", "/-/healthy", "", 90*time.Second); err != nil {
		return err
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "smoke", "running cluster Prometheus PromQL smoke query", map[string]any{
		"service": serviceName,
	})
	return h.waitForServiceProxySuccess(ctx, clusterID, req.Namespace, serviceName, "9090", "/api/v1/query", "query=vector(1)", 90*time.Second)
}

func (h *MonitoringHandler) waitForServiceProxySuccess(ctx context.Context, clusterID, namespace, serviceName, port, path, rawQuery string, timeout time.Duration) error {
	if h.requester == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		lastErr = h.serviceProxyCheck(ctx, clusterID, namespace, serviceName, port, path, rawQuery)
		if lastErr == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("service readiness check timed out for %s/%s: %w", namespace, serviceName, lastErr)
		}
		time.Sleep(5 * time.Second)
	}
}

func (h *MonitoringHandler) serviceProxyCheck(ctx context.Context, clusterID, namespace, serviceName, port, path, rawQuery string) error {
	target := serviceName + ":" + port
	proxyPath := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s/proxy%s", namespace, target, path)
	if rawQuery != "" {
		proxyPath += "?" + rawQuery
	}
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, proxyPath, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	return ensureSuccess(resp)
}

func (h *MonitoringHandler) findPrometheusServiceName(ctx context.Context, clusterID, namespace, releaseName string) (string, error) {
	if h.requester == nil {
		return "", fmt.Errorf("kubernetes requester not configured")
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/services?labelSelector=%s", namespace, url.QueryEscape("app.kubernetes.io/instance="+releaseName))
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return "", err
	}
	if err := ensureSuccess(resp); err != nil {
		return "", err
	}
	var payload map[string]any
	if err := parseJSONResponse(resp, &payload); err != nil {
		return "", err
	}
	for _, item := range objectItems(payload) {
		meta, _ := item["metadata"].(map[string]any)
		spec, _ := item["spec"].(map[string]any)
		name, _ := meta["name"].(string)
		if name == "" {
			continue
		}
		if strings.Contains(strings.ToLower(name), "prometheus") && serviceExposesPort(spec, 9090) {
			return name, nil
		}
	}
	return "", fmt.Errorf("prometheus service not found for release %s", releaseName)
}

func serviceExposesPort(spec map[string]any, port int) bool {
	ports, _ := spec["ports"].([]any)
	for _, item := range ports {
		entry, _ := item.(map[string]any)
		if entry == nil {
			continue
		}
		if intValue(entry, "port") == port || intValue(entry, "targetPort") == port {
			return true
		}
	}
	return false
}

func nullablePgTime(ts pgtype.Timestamptz) any {
	if !ts.Valid {
		return nil
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

func (h *MonitoringHandler) UnmarshalBody(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

// --- Monitoring endpoints CRUD (Python: /api/v1/monitoring/endpoints/) ---
//
// We back this on the existing `monitoring_backends` table since the Python
// `PrometheusEndpoint` model maps to the same configuration concept.

// ListEndpoints handles GET /api/v1/monitoring/endpoints/.
func (h *MonitoringHandler) ListEndpoints(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondPaginated(w, r, []any{}, 0)
		return
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil && err != pgx.ErrNoRows {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to load monitoring endpoints")
		return
	}
	items := []map[string]any{}
	if err == nil && backend.ID != uuid.Nil {
		items = append(items, monitoringBackendResponse(backend))
	}
	RespondPaginated(w, r, items, int64(len(items)))
}

// GetEndpoint handles GET /api/v1/monitoring/endpoints/{id}/.
func (h *MonitoringHandler) GetEndpoint(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "monitoring_error", "monitoring store not configured")
		return
	}
	idStr := chi.URLParam(r, "id")
	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Monitoring endpoint not found")
		return
	}
	if backend.ID.String() != idStr {
		RespondError(w, http.StatusNotFound, "not_found", "Monitoring endpoint not found")
		return
	}
	RespondJSON(w, http.StatusOK, monitoringBackendResponse(backend))
}

// CreateEndpoint handles POST /api/v1/monitoring/endpoints/.
// Currently maps to UpsertDefaultMonitoringBackend.
func (h *MonitoringHandler) CreateEndpoint(w http.ResponseWriter, r *http.Request) {
	h.UpdateBackendConfig(w, r)
}

// UpdateEndpoint handles PUT /api/v1/monitoring/endpoints/{id}/.
func (h *MonitoringHandler) UpdateEndpoint(w http.ResponseWriter, r *http.Request) {
	h.UpdateBackendConfig(w, r)
}

// DeleteEndpoint handles DELETE /api/v1/monitoring/endpoints/{id}/.
// We do not currently support deleting the default backend; return 501.
func (h *MonitoringHandler) DeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	RespondError(w, http.StatusNotImplemented, "not_implemented", "Deleting the default monitoring backend is not yet supported")
}

// --- Legacy path aliases ---
// Python registered the metrics ViewSet at /api/v1/monitoring/metrics/{action}/{cluster_id}/...
// These wrappers extract the path params and delegate to the existing
// cluster-scoped handlers.

// LegacyMetricsQuery proxies POST /api/v1/monitoring/metrics/query/{cluster_id}/.
func (h *MonitoringHandler) LegacyMetricsQuery(w http.ResponseWriter, r *http.Request) {
	h.PrometheusQuery(w, r)
}

// LegacyClusterOverview proxies GET /api/v1/monitoring/metrics/cluster-overview/{cluster_id}/.
func (h *MonitoringHandler) LegacyClusterOverview(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	if summary, ok, err := h.realClusterSummary(r.Context(), clusterID); err == nil && ok {
		RespondJSON(w, http.StatusOK, map[string]any{"status": "success", "data": summary})
		return
	}
	summary, err := h.clusterSummary(r.Context(), clusterID)
	if err != nil {
		RespondJSON(w, http.StatusBadGateway, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"status": "success", "data": summary})
}

// LegacyWorkloadMetrics proxies GET /api/v1/monitoring/metrics/workload/{cluster_id}/{namespace}/{workload}/.
func (h *MonitoringHandler) LegacyWorkloadMetrics(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	namespace := chi.URLParam(r, "namespace")
	workload := chi.URLParam(r, "workload")
	if data, ok, err := h.realWorkloadMetrics(r.Context(), clusterID, "", namespace, workload, r.URL.Query().Get("range")); err == nil && ok {
		RespondJSON(w, http.StatusOK, map[string]any{"status": "success", "data": data})
		return
	}
	summary, err := h.workloadSummary(r.Context(), clusterID, "", namespace, workload)
	if err != nil {
		RespondJSON(w, http.StatusBadGateway, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"status": "success", "data": h.metricsSeries(summary, r.URL.Query().Get("range"), namespace+"/"+workload)})
}

// LegacyNodeMetrics proxies GET /api/v1/monitoring/metrics/node/{cluster_id}/{node}/.
// Returns 501 because there is no cluster-scoped node metrics endpoint yet.
func (h *MonitoringHandler) LegacyNodeMetrics(w http.ResponseWriter, r *http.Request) {
	// TODO: wire up to a cluster-node metrics endpoint once a Go-side equivalent exists.
	RespondError(w, http.StatusNotImplemented, "not_implemented", "node metrics not yet implemented")
}
