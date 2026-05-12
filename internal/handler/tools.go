package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type ToolQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	GetClusterToolByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterTool, error)
	GetToolBySlug(ctx context.Context, slug string) (sqlc.ClusterTool, error)
	ListClusterTools(ctx context.Context, arg sqlc.ListClusterToolsParams) ([]sqlc.ClusterTool, error)
	ListEnabledTools(ctx context.Context) ([]sqlc.ClusterTool, error)
	CountClusterTools(ctx context.Context) (int64, error)
	CountInstalledCharts(ctx context.Context) (int64, error)
	ListInstalledChartsByCluster(ctx context.Context, arg sqlc.ListInstalledChartsByClusterParams) ([]sqlc.InstalledChart, error)
	GetInstalledChartByRelease(ctx context.Context, arg sqlc.GetInstalledChartByReleaseParams) (sqlc.InstalledChart, error)
	CreateInstalledChart(ctx context.Context, arg sqlc.CreateInstalledChartParams) (sqlc.InstalledChart, error)
	UpdateInstalledChartStatus(ctx context.Context, arg sqlc.UpdateInstalledChartStatusParams) error
	AdoptInstalledChartByRelease(ctx context.Context, arg sqlc.AdoptInstalledChartByReleaseParams) (sqlc.InstalledChart, error)
	UpdateInstalledChartValues(ctx context.Context, arg sqlc.UpdateInstalledChartValuesParams) (sqlc.InstalledChart, error)
	DeleteInstalledChart(ctx context.Context, id uuid.UUID) error
	CreateToolOperation(ctx context.Context, arg sqlc.CreateToolOperationParams) (sqlc.ToolOperation, error)
	GetToolOperation(ctx context.Context, id uuid.UUID) (sqlc.ToolOperation, error)
	ListToolOperations(ctx context.Context, arg sqlc.ListToolOperationsParams) ([]sqlc.ToolOperation, error)
	ListPendingToolOperations(ctx context.Context, limit int32) ([]sqlc.ToolOperation, error)
	GetLatestToolOperationForTarget(ctx context.Context, arg sqlc.GetLatestToolOperationForTargetParams) (sqlc.ToolOperation, error)
	MarkToolOperationRunning(ctx context.Context, id uuid.UUID) (sqlc.ToolOperation, error)
	MarkToolOperationCompleted(ctx context.Context, id uuid.UUID) (sqlc.ToolOperation, error)
	MarkToolOperationFailed(ctx context.Context, arg sqlc.MarkToolOperationFailedParams) (sqlc.ToolOperation, error)
	MarkToolOperationSuperseded(ctx context.Context, arg sqlc.MarkToolOperationSupersededParams) (sqlc.ToolOperation, error)
	RequeueToolOperation(ctx context.Context, id uuid.UUID) (sqlc.ToolOperation, error)
	CreateToolOperationEvent(ctx context.Context, arg sqlc.CreateToolOperationEventParams) (sqlc.ToolOperationEvent, error)
	ListToolOperationEvents(ctx context.Context, operationID uuid.UUID) ([]sqlc.ToolOperationEvent, error)
}

type ToolHandler struct {
	queries ToolQuerier
	helm    HelmRequester
	log     *slog.Logger
	authz   authorizationSupport
	mu      sync.Mutex
	trigger chan struct{}
	// helmConcurrency caps the number of executeOperation goroutines
	// dispatched per reconciler tick.
	helmConcurrency int
}

func NewToolHandler(queries ToolQuerier) *ToolHandler {
	return &ToolHandler{
		queries: queries,
		log:     slog.Default(),
		trigger: make(chan struct{}, 1),
	}
}

func NewToolHandlerWithHelm(queries ToolQuerier, helm HelmRequester) *ToolHandler {
	return &ToolHandler{
		queries: queries,
		helm:    helm,
		log:     slog.Default(),
		trigger: make(chan struct{}, 1),
	}
}

type ToolResponse struct {
	ID                string          `json:"id"`
	Slug              string          `json:"slug"`
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	Icon              string          `json:"icon"`
	Category          string          `json:"category"`
	Charts            json.RawMessage `json:"charts"`
	VersionConstraint string          `json:"version_constraint"`
	DefaultNamespace  string          `json:"default_namespace"`
	IsBuiltin         bool            `json:"is_builtin"`
	IsEnabled         bool            `json:"is_enabled"`
	HelmChartID       *string         `json:"helm_chart_id"`
	Presets           json.RawMessage `json:"presets"`
	ServiceName       string          `json:"service_name"`
	ServicePort       *int32          `json:"service_port"`
	ServicePath       string          `json:"service_path"`
	SubServices       json.RawMessage `json:"sub_services"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
}

func toolToResponse(t sqlc.ClusterTool) ToolResponse {
	resp := ToolResponse{
		ID:                t.ID.String(),
		Slug:              t.Slug,
		Name:              t.Name,
		Description:       t.Description,
		Icon:              t.Icon,
		Category:          t.Category,
		Charts:            t.Charts,
		VersionConstraint: t.VersionConstraint,
		DefaultNamespace:  t.DefaultNamespace,
		IsBuiltin:         t.IsBuiltin,
		IsEnabled:         t.IsEnabled,
		Presets:           t.Presets,
		ServiceName:       t.ServiceName,
		ServicePath:       t.ServicePath,
		SubServices:       t.SubServices,
		CreatedAt:         t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:         t.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if t.HelmChartID.Valid {
		s := uuid.UUID(t.HelmChartID.Bytes).String()
		resp.HelmChartID = &s
	}
	if t.ServicePort.Valid {
		resp.ServicePort = &t.ServicePort.Int32
	}
	return resp
}

type toolChart struct {
	ChartName string `json:"chart_name"`
	RepoURL   string `json:"repo_url"`
	Namespace string `json:"namespace"`
	Order     int    `json:"order"`
}

type toolActionRequest struct {
	ClusterID      string `json:"cluster_id"`
	Preset         string `json:"preset"`
	ValuesOverride string `json:"values_override"`
	ReleaseName    string `json:"release_name"`
}

type toolOperationEnvelope struct {
	ClusterID      string     `json:"clusterId"`
	ToolSlug       string     `json:"toolSlug"`
	ReleaseName    string     `json:"releaseName,omitempty"`
	Namespace      string     `json:"namespace,omitempty"`
	Preset         string     `json:"preset,omitempty"`
	ValuesYAML     string     `json:"valuesYaml,omitempty"`
	ChartName      string     `json:"chartName,omitempty"`
	RepoURL        string     `json:"repoUrl,omitempty"`
	Version        string     `json:"version,omitempty"`
	InstalledChart *uuid.UUID `json:"installedChartId,omitempty"`
}

func (h *ToolHandler) SetLogger(log *slog.Logger) {
	if log != nil {
		h.log = log
	}
}

func (h *ToolHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	h.authz.SetAuthorization(engine, querier)
}

func (h *ToolHandler) StartReconciler(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	go h.runReconciler(ctx)
}

func (h *ToolHandler) TriggerReconcile() {
	if h == nil || h.trigger == nil {
		return
	}
	select {
	case h.trigger <- struct{}{}:
	default:
	}
}

// EnsureInstalled synchronously installs or adopts a tool release on the given
// cluster. It is intended for platform-owned bootstrap flows, where waiting
// for the async operation queue would only add startup lag and complexity.
func (h *ToolHandler) EnsureInstalled(ctx context.Context, clusterID uuid.UUID, slug, releaseName, preset, valuesYAML string) (sqlc.InstalledChart, error) {
	if h == nil || h.queries == nil {
		return sqlc.InstalledChart{}, errors.New("tool handler not configured")
	}
	tool, err := h.queries.GetToolBySlug(ctx, slug)
	if err != nil {
		return sqlc.InstalledChart{}, err
	}
	if releaseName == "" {
		releaseName = slug
	}
	charts, _ := parseToolCharts(tool.Charts)
	chart := firstChart(charts)
	namespace := chartNamespace(tool, chart)
	if item, err := h.findInstalledTool(ctx, clusterID, slug); err == nil {
		return item, nil
	} else if !errors.Is(err, errInstalledChartNotFound) {
		return sqlc.InstalledChart{}, err
	}

	env := toolOperationEnvelope{
		ClusterID:   clusterID.String(),
		ToolSlug:    slug,
		ReleaseName: releaseName,
		Namespace:   namespace,
		Preset:      preset,
		ValuesYAML:  valuesYAML,
		ChartName:   chart.ChartName,
		RepoURL:     chart.RepoURL,
		Version:     tool.VersionConstraint,
	}
	if status, exists, err := existingHelmReleaseStatus(ctx, h.helm, env.ClusterID, env.ReleaseName, env.Namespace); err != nil {
		return sqlc.InstalledChart{}, err
	} else if exists {
		if err := adoptExistingToolRelease(ctx, h.queries, clusterID, env, status); err != nil {
			return sqlc.InstalledChart{}, err
		}
	} else {
		result, err := h.sendHelmRaw(ctx, env, protocol.MsgHelmInstall)
		if err != nil {
			return sqlc.InstalledChart{}, err
		}
		if _, err := h.queries.CreateInstalledChart(ctx, sqlc.CreateInstalledChartParams{
			ClusterID:      clusterID,
			ReleaseName:    env.ReleaseName,
			Namespace:      env.Namespace,
			ValuesOverride: env.ValuesYAML,
			Status:         normalizeToolStatus(result.Status),
			Revision:       int32(result.Revision),
			ToolSlug:       pgtype.Text{String: env.ToolSlug, Valid: true},
			PresetUsed:     pgtype.Text{String: env.Preset, Valid: env.Preset != ""},
		}); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return h.findInstalledTool(ctx, clusterID, slug)
			}
			return sqlc.InstalledChart{}, err
		}
	}
	return h.findInstalledTool(ctx, clusterID, slug)
}

func (h *ToolHandler) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	h.processPendingOperations(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.processPendingOperations(ctx)
		case <-h.trigger:
			h.processPendingOperations(ctx)
		}
	}
}

func (h *ToolHandler) List(w http.ResponseWriter, r *http.Request) {
	tools, err := h.queries.ListClusterTools(r.Context(), sqlc.ListClusterToolsParams{
		Limit:  int32(queryInt(r, "limit", 20)),
		Offset: int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list tools")
		return
	}
	total, _ := h.queries.CountClusterTools(r.Context())
	items := make([]ToolResponse, 0, len(tools))
	for _, t := range tools {
		items = append(items, toolToResponse(t))
	}
	RespondPaginated(w, r, items, total)
}

func (h *ToolHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err == nil {
		tool, err := h.queries.GetClusterToolByID(r.Context(), id)
		if err == nil {
			RespondJSON(w, http.StatusOK, toolToResponse(tool))
			return
		}
	}
	h.GetBySlug(w, r)
}

func (h *ToolHandler) GetBySlug(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	tool, err := h.queries.GetToolBySlug(r.Context(), slug)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Tool not found")
		return
	}
	RespondJSON(w, http.StatusOK, toolToResponse(tool))
}

func (h *ToolHandler) Preview(w http.ResponseWriter, r *http.Request) {
	tool, req, chart, valuesYAML, err := h.resolveAction(r)
	if err != nil {
		if errors.Is(err, errToolNotFound) {
			RespondError(w, http.StatusNotFound, "not_found", "Tool not found")
			return
		}
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"charts": []map[string]any{{
			"chart_name":    chart.ChartName,
			"chart_version": tool.VersionConstraint,
			"namespace":     chartNamespace(tool, chart),
			"values_yaml":   valuesYAML,
		}},
		"preset": req.Preset,
	})
}

func (h *ToolHandler) Install(w http.ResponseWriter, r *http.Request) {
	tool, req, chart, valuesYAML, err := h.resolveAction(r)
	if err != nil {
		if errors.Is(err, errToolNotFound) {
			RespondError(w, http.StatusNotFound, "not_found", "Tool not found")
			return
		}
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbCreate) {
		return
	}
	releaseName := req.ReleaseName
	if releaseName == "" {
		releaseName = tool.Slug
	}
	if _, err := h.findInstalledTool(r.Context(), clusterID, tool.Slug); err == nil {
		RespondError(w, http.StatusConflict, "already_installed", "Tool is already installed on cluster")
		return
	} else if !errors.Is(err, errInstalledChartNotFound) {
		RespondError(w, http.StatusInternalServerError, "lookup_error", "Failed to lookup installed tool")
		return
	}
	if msg, ok := h.checkToolScope(r.Context(), tool.Slug, clusterID); !ok {
		RespondError(w, http.StatusBadRequest, "wrong_cluster_scope", msg)
		return
	}
	op, err := h.enqueueOperation(r.Context(), "tool_installation", operationTargetKey(clusterID, tool.Slug), "install", toolOperationEnvelope{
		ClusterID:   req.ClusterID,
		ToolSlug:    tool.Slug,
		ReleaseName: releaseName,
		Namespace:   chartNamespace(tool, chart),
		Preset:      req.Preset,
		ValuesYAML:  valuesYAML,
		ChartName:   chart.ChartName,
		RepoURL:     chart.RepoURL,
		Version:     tool.VersionConstraint,
	}, currentUserUUID(r))
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "enqueue_error", "Failed to enqueue tool installation")
		return
	}
	recordAudit(r, h.queries, "tool.install", "tool", tool.ID.String(), tool.Slug, map[string]any{
		"cluster_id":   req.ClusterID,
		"release_name": releaseName,
		"chart":        chart.ChartName,
		"version":      tool.VersionConstraint,
		"preset":       req.Preset,
		"operation_id": op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, toolOperationResponse(op))
}

func (h *ToolHandler) Upgrade(w http.ResponseWriter, r *http.Request) {
	tool, req, chart, valuesYAML, err := h.resolveAction(r)
	if err != nil {
		if errors.Is(err, errToolNotFound) {
			RespondError(w, http.StatusNotFound, "not_found", "Tool not found")
			return
		}
		RespondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbUpdate) {
		return
	}
	existing, err := h.findInstalledTool(r.Context(), clusterID, tool.Slug)
	if err != nil {
		if errors.Is(err, errInstalledChartNotFound) {
			RespondError(w, http.StatusNotFound, "not_found", "Installed tool not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, "lookup_error", "Failed to lookup installed tool")
		return
	}
	releaseName := existing.ReleaseName
	chartID := existing.ID
	op, err := h.enqueueOperation(r.Context(), "tool_installation", operationTargetKey(clusterID, tool.Slug), "upgrade", toolOperationEnvelope{
		ClusterID:      req.ClusterID,
		ToolSlug:       tool.Slug,
		ReleaseName:    releaseName,
		Namespace:      existing.Namespace,
		Preset:         req.Preset,
		ValuesYAML:     valuesYAML,
		ChartName:      chart.ChartName,
		RepoURL:        chart.RepoURL,
		Version:        tool.VersionConstraint,
		InstalledChart: &chartID,
	}, currentUserUUID(r))
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "enqueue_error", "Failed to enqueue tool upgrade")
		return
	}
	recordAudit(r, h.queries, "tool.upgrade", "tool", tool.ID.String(), tool.Slug, map[string]any{
		"cluster_id":   req.ClusterID,
		"release_name": releaseName,
		"chart":        chart.ChartName,
		"version":      tool.VersionConstraint,
		"preset":       req.Preset,
		"operation_id": op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, toolOperationResponse(op))
}

func (h *ToolHandler) Uninstall(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	_, err := h.queries.GetToolBySlug(r.Context(), slug)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Tool not found")
		return
	}
	var req toolActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbDelete) {
		return
	}
	existing, err := h.findInstalledTool(r.Context(), clusterID, slug)
	if err != nil {
		if errors.Is(err, errInstalledChartNotFound) {
			RespondError(w, http.StatusNotFound, "not_found", "Installed tool not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, "lookup_error", "Failed to lookup installed tool")
		return
	}
	chartID := existing.ID
	op, err := h.enqueueOperation(r.Context(), "tool_installation", operationTargetKey(clusterID, slug), "uninstall", toolOperationEnvelope{
		ClusterID:      req.ClusterID,
		ToolSlug:       slug,
		ReleaseName:    existing.ReleaseName,
		Namespace:      existing.Namespace,
		InstalledChart: &chartID,
	}, currentUserUUID(r))
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "enqueue_error", "Failed to enqueue tool uninstall")
		return
	}
	recordAudit(r, h.queries, "tool.uninstall", "tool", existing.ID.String(), slug, map[string]any{
		"cluster_id":   req.ClusterID,
		"release_name": existing.ReleaseName,
		"namespace":    existing.Namespace,
		"operation_id": op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, toolOperationResponse(op))
}

func (h *ToolHandler) Adopt(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	tool, err := h.queries.GetToolBySlug(r.Context(), slug)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Tool not found")
		return
	}
	var req toolActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbCreate) {
		return
	}
	if req.ReleaseName == "" {
		req.ReleaseName = tool.Slug
	}
	charts, _ := parseToolCharts(tool.Charts)
	chart := firstChart(charts)
	op, err := h.enqueueOperation(r.Context(), "tool_installation", operationTargetKey(clusterID, slug), "adopt", toolOperationEnvelope{
		ClusterID:   req.ClusterID,
		ToolSlug:    slug,
		ReleaseName: req.ReleaseName,
		Namespace:   chartNamespace(tool, chart),
	}, currentUserUUID(r))
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "enqueue_error", "Failed to enqueue tool adoption")
		return
	}
	recordAudit(r, h.queries, "tool.adopt", "tool", tool.ID.String(), slug, map[string]any{
		"cluster_id":   req.ClusterID,
		"release_name": req.ReleaseName,
		"namespace":    chartNamespace(tool, chart),
		"operation_id": op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, toolOperationResponse(op))
}

func (h *ToolHandler) ClusterStatus(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	tools, err := h.queries.ListEnabledTools(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list tools")
		return
	}
	installed, _ := h.queries.ListInstalledChartsByCluster(r.Context(), sqlc.ListInstalledChartsByClusterParams{
		ClusterID: clusterID,
		Limit:     200,
		Offset:    0,
	})
	bySlug := map[string]sqlc.InstalledChart{}
	for _, item := range installed {
		if item.ToolSlug.Valid {
			bySlug[item.ToolSlug.String] = item
		}
	}
	pendingOps, _ := h.queries.ListToolOperations(r.Context(), sqlc.ListToolOperationsParams{
		Limit:  200,
		Offset: 0,
		Status: pgtype.Text{String: OpStatusPending, Valid: true},
	})
	runningOps, _ := h.queries.ListToolOperations(r.Context(), sqlc.ListToolOperationsParams{
		Limit:  200,
		Offset: 0,
		Status: pgtype.Text{String: OpStatusRunning, Valid: true},
	})
	opBySlug := map[string]sqlc.ToolOperation{}
	for _, op := range append(pendingOps, runningOps...) {
		var env toolOperationEnvelope
		if json.Unmarshal(op.Payload, &env) != nil {
			continue
		}
		if env.ClusterID != clusterID.String() {
			continue
		}
		if existing, ok := opBySlug[env.ToolSlug]; ok && existing.CreatedAt.After(op.CreatedAt) {
			continue
		}
		opBySlug[env.ToolSlug] = op
	}
	statuses := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		status := map[string]any{
			"slug":         tool.Slug,
			"name":         tool.Name,
			"status":       "not_installed",
			"release_name": nil,
			"namespace":    nil,
			"preset_used":  nil,
			"error":        nil,
		}
		if item, ok := bySlug[tool.Slug]; ok {
			status["status"] = toolStatusFromInstalled(item.Status)
			status["release_name"] = item.ReleaseName
			status["namespace"] = item.Namespace
			if item.PresetUsed.Valid {
				status["preset_used"] = item.PresetUsed.String
			}
		}
		if op, ok := opBySlug[tool.Slug]; ok {
			status["operation"] = toolOperationResponse(op)
			switch op.OperationType {
			case "install", "adopt":
				status["status"] = "installing"
			case "upgrade":
				status["status"] = "upgrading"
			case "uninstall":
				status["status"] = "uninstalling"
			}
		}
		statuses = append(statuses, status)
	}
	RespondJSON(w, http.StatusOK, statuses)
}

func (h *ToolHandler) ListOperations(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 50))
	offset := int32(queryInt(r, "offset", 0))
	arg := sqlc.ListToolOperationsParams{Limit: limit, Offset: offset}
	if v := strings.TrimSpace(r.URL.Query().Get("targetType")); v != "" {
		arg.TargetType = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("targetKey")); v != "" {
		arg.TargetKey = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		arg.Status = pgtype.Text{String: v, Valid: true}
	}
	ops, err := h.queries.ListToolOperations(r.Context(), arg)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list tool operations")
		return
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "permission_error", "Failed to retrieve user permissions")
		return
	}
	items := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		if restricted {
			clusterID, err := toolOperationClusterID(op)
			if err != nil || !h.authz.allowsCluster(bindings, clusterID, rbac.ResourceCatalog, rbac.VerbRead) {
				continue
			}
		}
		items = append(items, toolOperationResponse(op))
	}
	RespondJSON(w, http.StatusOK, items)
}

func (h *ToolHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid operation ID")
		return
	}
	op, err := h.queries.GetToolOperation(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Tool operation not found")
		return
	}
	clusterID, err := toolOperationClusterID(op)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "resolve_error", "Failed to resolve tool operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}
	resp := toolOperationResponse(op)
	if events, err := h.queries.ListToolOperationEvents(r.Context(), op.ID); err == nil {
		resp["events"] = toolOperationEventsResponse(events)
	}
	RespondJSON(w, http.StatusOK, resp)
}

func (h *ToolHandler) RetryOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid operation ID")
		return
	}
	op, err := h.queries.GetToolOperation(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Tool operation not found")
		return
	}
	if op.Status != OpStatusFailed && op.Status != OpStatusSuperseded {
		RespondError(w, http.StatusConflict, "invalid_state", "Only failed or superseded operations can be retried")
		return
	}
	clusterID, err := toolOperationClusterID(op)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "resolve_error", "Failed to resolve tool operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbUpdate) {
		return
	}
	requeued, err := h.queries.RequeueToolOperation(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "retry_error", "Failed to retry tool operation")
		return
	}
	h.TriggerReconcile()
	recordAudit(r, h.queries, "tool.operation.retry", "tool_operation", id.String(), op.TargetKey, map[string]any{
		"target_type":     op.TargetType,
		"previous_status": op.Status,
	})
	RespondJSON(w, http.StatusAccepted, toolOperationResponse(requeued))
}

func toolOperationClusterID(op sqlc.ToolOperation) (uuid.UUID, error) {
	var env toolOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return uuid.UUID{}, err
	}
	return uuid.Parse(env.ClusterID)
}

func (h *ToolHandler) ControllerStatus(w http.ResponseWriter, r *http.Request) {
	summary, err := h.controllerSummary(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "status_error", "Failed to load tool operations")
		return
	}
	RespondJSON(w, http.StatusOK, summary)
}

func (h *ToolHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	ops, err := h.queries.ListToolOperations(ctx, sqlc.ListToolOperationsParams{Limit: 1000, Offset: 0})
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
			clusterID, err := toolOperationClusterID(op)
			if err != nil || !h.authz.allowsCluster(bindings, clusterID, rbac.ResourceCatalog, rbac.VerbRead) {
				continue
			}
		}
		counts[op.Status]++
		if op.Status == OpStatusRunning && op.StartedAt.Valid && time.Since(op.StartedAt.Time) > time.Minute {
			staleRunning++
		}
		if len(recent) < 5 {
			recent = append(recent, h.operationPreview(ctx, op))
		}
		if (op.Status == OpStatusFailed || op.Status == OpStatusSuperseded) && time.Since(op.CreatedAt) <= 30*time.Minute {
			recentFailureCount++
		}
		if latestFailure == nil && (op.Status == OpStatusFailed || op.Status == OpStatusSuperseded) {
			latestFailure = h.operationPreview(ctx, op)
		}
	}
	toolCount, _ := h.queries.CountClusterTools(ctx)
	installedCount, _ := h.queries.CountInstalledCharts(ctx)
	return map[string]any{
		"reconciler": map[string]any{
			"enabled":              true,
			"queueDepth":           counts[OpStatusPending] + counts[OpStatusRunning],
			"staleRunningCount":    staleRunning,
			"staleThresholdSecond": 60,
		},
		"tools": map[string]any{
			"catalogCount": toolCount,
			"installedCount": func() any {
				if restricted {
					return nil
				}
				return installedCount
			}(),
		},
		"operations":         counts,
		"recentFailureCount": recentFailureCount,
		"recentOperations":   recent,
		"latestFailure":      latestFailure,
	}, nil
}

// errToolNotFound is returned by resolveAction when the slug doesn't match any
// tool. Callers translate this into a 404 with a clean "Tool not found" body
// rather than leaking pgx's "no rows in result set" string to the API client.
var errToolNotFound = errors.New("tool not found")

func (h *ToolHandler) resolveAction(r *http.Request) (sqlc.ClusterTool, toolActionRequest, toolChart, string, error) {
	tool, err := h.queries.GetToolBySlug(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.ClusterTool{}, toolActionRequest{}, toolChart{}, "", errToolNotFound
		}
		return sqlc.ClusterTool{}, toolActionRequest{}, toolChart{}, "", err
	}
	var req toolActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return sqlc.ClusterTool{}, toolActionRequest{}, toolChart{}, "", err
	}
	if _, err := uuid.Parse(req.ClusterID); err != nil {
		return sqlc.ClusterTool{}, toolActionRequest{}, toolChart{}, "", err
	}
	charts, err := parseToolCharts(tool.Charts)
	if err != nil || len(charts) == 0 {
		if err == nil {
			err = errors.New("tool has no charts configured")
		}
		return sqlc.ClusterTool{}, toolActionRequest{}, toolChart{}, "", err
	}
	valuesYAML := presetValuesYAML(tool.Presets, req.Preset)
	if req.ValuesOverride != "" {
		if valuesYAML != "" {
			valuesYAML += "\n"
		}
		valuesYAML += req.ValuesOverride
	}
	return tool, req, firstChart(charts), valuesYAML, nil
}

func (h *ToolHandler) sendHelm(ctx context.Context, clusterID string, msgType protocol.MessageType, tool sqlc.ClusterTool, chart toolChart, releaseName, valuesYAML string) (*protocol.HelmResultPayload, error) {
	if h.helm == nil {
		return nil, errors.New("helm requester not configured")
	}
	var values map[string]any
	if valuesYAML != "" {
		if err := yaml.Unmarshal([]byte(valuesYAML), &values); err != nil {
			return nil, err
		}
	}
	return h.helm.Do(ctx, clusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: releaseName,
		Namespace:   chartNamespace(tool, chart),
		ChartName:   chart.ChartName,
		RepoURL:     chart.RepoURL,
		Version:     tool.VersionConstraint,
		Values:      values,
	})
}

func (h *ToolHandler) sendHelmRaw(ctx context.Context, env toolOperationEnvelope, msgType protocol.MessageType) (*protocol.HelmResultPayload, error) {
	if h.helm == nil {
		return nil, errors.New("helm requester not configured")
	}
	var values map[string]any
	if env.ValuesYAML != "" {
		if err := yaml.Unmarshal([]byte(env.ValuesYAML), &values); err != nil {
			return nil, err
		}
	}
	return h.helm.Do(ctx, env.ClusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: env.ReleaseName,
		Namespace:   env.Namespace,
		ChartName:   env.ChartName,
		RepoURL:     env.RepoURL,
		Version:     env.Version,
		Values:      values,
	})
}

var errInstalledChartNotFound = errors.New("installed chart not found")

func (h *ToolHandler) findInstalledTool(ctx context.Context, clusterID uuid.UUID, slug string) (sqlc.InstalledChart, error) {
	items, err := h.queries.ListInstalledChartsByCluster(ctx, sqlc.ListInstalledChartsByClusterParams{
		ClusterID: clusterID,
		Limit:     200,
		Offset:    0,
	})
	if err != nil {
		return sqlc.InstalledChart{}, err
	}
	for _, item := range items {
		if item.ToolSlug.Valid && item.ToolSlug.String == slug {
			return item, nil
		}
	}
	return sqlc.InstalledChart{}, errInstalledChartNotFound
}

func parseToolCharts(raw json.RawMessage) ([]toolChart, error) {
	var charts []toolChart
	err := json.Unmarshal(raw, &charts)
	return charts, err
}

func firstChart(charts []toolChart) toolChart {
	if len(charts) == 0 {
		return toolChart{}
	}
	best := charts[0]
	for _, chart := range charts[1:] {
		if chart.Order < best.Order {
			best = chart
		}
	}
	return best
}

func chartNamespace(tool sqlc.ClusterTool, chart toolChart) string {
	if chart.Namespace != "" {
		return chart.Namespace
	}
	return tool.DefaultNamespace
}

func presetValuesYAML(raw json.RawMessage, preset string) string {
	if preset == "" {
		return ""
	}
	var presets map[string]any
	if json.Unmarshal(raw, &presets) != nil {
		return ""
	}
	value, ok := presets[preset]
	if !ok {
		return ""
	}
	data, _ := yaml.Marshal(value)
	return string(data)
}

func normalizeToolStatus(status string) string {
	switch status {
	case "deployed":
		return "installed"
	default:
		return status
	}
}

func toolStatusFromInstalled(status string) string {
	switch status {
	case "pending", "pending_install":
		return "installing"
	case "deployed":
		return "installed"
	default:
		return status
	}
}

type toolInstallPersister interface {
	GetInstalledChartByRelease(ctx context.Context, arg sqlc.GetInstalledChartByReleaseParams) (sqlc.InstalledChart, error)
	CreateInstalledChart(ctx context.Context, arg sqlc.CreateInstalledChartParams) (sqlc.InstalledChart, error)
	AdoptInstalledChartByRelease(ctx context.Context, arg sqlc.AdoptInstalledChartByReleaseParams) (sqlc.InstalledChart, error)
}

func (h *ToolHandler) enqueueOperation(ctx context.Context, targetType, targetKey, operationType string, env toolOperationEnvelope, userID pgtype.UUID) (sqlc.ToolOperation, error) {
	payload, err := json.Marshal(env)
	if err != nil {
		return sqlc.ToolOperation{}, err
	}
	op, err := h.queries.CreateToolOperation(ctx, sqlc.CreateToolOperationParams{
		TargetType:    targetType,
		TargetKey:     targetKey,
		OperationType: operationType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	})
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

func toolOperationResponse(op sqlc.ToolOperation) map[string]any {
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

func toolOperationEventsResponse(events []sqlc.ToolOperationEvent) []map[string]any {
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

func (h *ToolHandler) operationPreview(ctx context.Context, op sqlc.ToolOperation) map[string]any {
	resp := toolOperationResponse(op)
	if events, err := h.queries.ListToolOperationEvents(ctx, op.ID); err == nil && len(events) > 0 {
		resp["eventsPreview"] = toolOperationEventsResponse(lastToolEvents(events, 3))
	}
	return resp
}

func lastToolEvents(events []sqlc.ToolOperationEvent, n int) []sqlc.ToolOperationEvent {
	if len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}

func (h *ToolHandler) processPendingOperations(ctx context.Context) {
	// Claim under the lock, then release before
	// helm dispatch so unrelated clusters' operations are not stalled
	// behind a stuck install (helmTimeout = 10 minutes).
	claimed := h.claimPendingToolOperations(ctx)
	if len(claimed) == 0 {
		return
	}
	sem := make(chan struct{}, effectiveHelmConcurrency(h.helmConcurrency))
	var wg sync.WaitGroup
	for _, op := range claimed {
		wg.Add(1)
		op := op
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := h.executeOperation(ctx, op); err != nil {
				h.recordToolOperationEvent(ctx, op.ID, "error", "complete", "operation failed", map[string]any{"error": err.Error()})
				_, _ = h.queries.MarkToolOperationFailed(ctx, sqlc.MarkToolOperationFailedParams{
					ID:           op.ID,
					ErrorMessage: err.Error(),
				})
				if h.log != nil {
					h.log.Warn("tool operation failed", "id", op.ID.String(), "error", err)
				}
				return
			}
			h.recordToolOperationEvent(ctx, op.ID, "info", "complete", "operation completed", map[string]any{})
			_, _ = h.queries.MarkToolOperationCompleted(ctx, op.ID)
		}()
	}
	wg.Wait()
}

// claimPendingToolOperations holds h.mu just long enough to supersede
// stale rows and mark this tick's claims "running" in the DB. Returned
// rows are dispatched outside the lock.
func (h *ToolHandler) claimPendingToolOperations(ctx context.Context) []sqlc.ToolOperation {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListPendingToolOperations(ctx, 20)
	if err != nil {
		return nil
	}
	latestByTarget := map[string]uuid.UUID{}
	for i := len(ops) - 1; i >= 0; i-- {
		key := ops[i].TargetType + ":" + ops[i].TargetKey
		if _, ok := latestByTarget[key]; !ok {
			latestByTarget[key] = ops[i].ID
		}
	}
	claimed := make([]sqlc.ToolOperation, 0, len(ops))
	for _, op := range ops {
		key := op.TargetType + ":" + op.TargetKey
		if latestID, ok := latestByTarget[key]; ok && latestID != op.ID {
			h.recordToolOperationEvent(ctx, op.ID, "info", "queue", "operation superseded by newer desired state", map[string]any{
				"targetType": op.TargetType,
				"targetKey":  op.TargetKey,
			})
			_, _ = h.queries.MarkToolOperationSuperseded(ctx, sqlc.MarkToolOperationSupersededParams{
				ID:           op.ID,
				ErrorMessage: "superseded by newer operation for target",
			})
			continue
		}
		if op.Status == OpStatusRunning && op.StartedAt.Valid && time.Since(op.StartedAt.Time) < time.Minute {
			continue
		}
		running, err := h.queries.MarkToolOperationRunning(ctx, op.ID)
		if err != nil {
			continue
		}
		h.recordToolOperationEvent(ctx, running.ID, "info", "queue", "operation execution started", map[string]any{
			"operationType": running.OperationType,
			"targetType":    running.TargetType,
			"targetKey":     running.TargetKey,
			"attemptCount":  running.AttemptCount,
		})
		claimed = append(claimed, running)
	}
	return claimed
}

func (h *ToolHandler) executeOperation(ctx context.Context, op sqlc.ToolOperation) error {
	var env toolOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return err
	}
	clusterID, err := uuid.Parse(env.ClusterID)
	if err != nil {
		return err
	}
	switch op.OperationType {
	case "install":
		h.recordToolOperationEvent(ctx, op.ID, "info", "install", "installing tool release", map[string]any{
			"clusterId":   env.ClusterID,
			"toolSlug":    env.ToolSlug,
			"releaseName": env.ReleaseName,
			"namespace":   env.Namespace,
		})
		if _, err := h.findInstalledTool(ctx, clusterID, env.ToolSlug); err == nil {
			return fmt.Errorf("tool %s already installed on cluster", env.ToolSlug)
		} else if !errors.Is(err, errInstalledChartNotFound) {
			return err
		}
		status, exists, err := existingHelmReleaseStatus(ctx, h.helm, env.ClusterID, env.ReleaseName, env.Namespace)
		if err != nil {
			return err
		}
		if exists {
			h.recordToolOperationEvent(ctx, op.ID, "info", "adopt", "existing Helm release detected; adopting into Astronomer state", map[string]any{
				"clusterId":   env.ClusterID,
				"toolSlug":    env.ToolSlug,
				"releaseName": env.ReleaseName,
				"namespace":   env.Namespace,
				"status":      status.Status,
				"revision":    status.Revision,
			})
			return adoptExistingToolRelease(ctx, h.queries, clusterID, env, status)
		}
		result, err := h.sendHelmRaw(ctx, env, protocol.MsgHelmInstall)
		if err != nil {
			return err
		}
		_, err = h.queries.CreateInstalledChart(ctx, sqlc.CreateInstalledChartParams{
			ClusterID:      clusterID,
			ReleaseName:    env.ReleaseName,
			Namespace:      env.Namespace,
			ValuesOverride: env.ValuesYAML,
			Status:         normalizeToolStatus(result.Status),
			Revision:       int32(result.Revision),
			ToolSlug:       pgtype.Text{String: env.ToolSlug, Valid: true},
			PresetUsed:     pgtype.Text{String: env.Preset, Valid: env.Preset != ""},
		})
		return err
	case "upgrade":
		h.recordToolOperationEvent(ctx, op.ID, "info", "upgrade", "upgrading tool release", map[string]any{
			"clusterId":   env.ClusterID,
			"toolSlug":    env.ToolSlug,
			"releaseName": env.ReleaseName,
			"namespace":   env.Namespace,
		})
		result, err := h.sendHelmRaw(ctx, env, protocol.MsgHelmUpgrade)
		if err != nil {
			return err
		}
		if env.InstalledChart != nil {
			_, err = h.queries.UpdateInstalledChartValues(ctx, sqlc.UpdateInstalledChartValuesParams{
				ID:             *env.InstalledChart,
				ValuesOverride: env.ValuesYAML,
				Status:         normalizeToolStatus(result.Status),
			})
			return err
		}
		item, err := h.findInstalledTool(ctx, clusterID, env.ToolSlug)
		if err != nil {
			return err
		}
		_, err = h.queries.UpdateInstalledChartValues(ctx, sqlc.UpdateInstalledChartValuesParams{
			ID:             item.ID,
			ValuesOverride: env.ValuesYAML,
			Status:         normalizeToolStatus(result.Status),
		})
		return err
	case "uninstall":
		h.recordToolOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling tool release", map[string]any{
			"clusterId":   env.ClusterID,
			"toolSlug":    env.ToolSlug,
			"releaseName": env.ReleaseName,
			"namespace":   env.Namespace,
		})
		if h.helm == nil {
			return errors.New("helm requester not configured")
		}
		_, err := h.helm.Do(ctx, env.ClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{
			ReleaseName: env.ReleaseName,
			Namespace:   env.Namespace,
		})
		if err != nil {
			return err
		}
		if env.InstalledChart != nil {
			return h.queries.DeleteInstalledChart(ctx, *env.InstalledChart)
		}
		item, err := h.findInstalledTool(ctx, clusterID, env.ToolSlug)
		if err != nil {
			return err
		}
		return h.queries.DeleteInstalledChart(ctx, item.ID)
	case "adopt":
		h.recordToolOperationEvent(ctx, op.ID, "info", "adopt", "adopting existing tool release", map[string]any{
			"clusterId":   env.ClusterID,
			"toolSlug":    env.ToolSlug,
			"releaseName": env.ReleaseName,
			"namespace":   env.Namespace,
		})
		if _, err := h.findInstalledTool(ctx, clusterID, env.ToolSlug); err == nil {
			return fmt.Errorf("tool %s already installed on cluster", env.ToolSlug)
		} else if !errors.Is(err, errInstalledChartNotFound) {
			return err
		}
		_, err := h.queries.CreateInstalledChart(ctx, sqlc.CreateInstalledChartParams{
			ClusterID:      clusterID,
			ReleaseName:    env.ReleaseName,
			Namespace:      env.Namespace,
			ValuesOverride: env.ValuesYAML,
			Status:         "installed_unmanaged",
			Revision:       1,
			ToolSlug:       pgtype.Text{String: env.ToolSlug, Valid: true},
			PresetUsed:     pgtype.Text{String: env.Preset, Valid: env.Preset != ""},
		})
		return err
	default:
		return fmt.Errorf("unsupported tool operation type: %s", op.OperationType)
	}
}

func existingHelmReleaseStatus(ctx context.Context, helm HelmRequester, clusterID, releaseName, namespace string) (*protocol.HelmResultPayload, bool, error) {
	if helm == nil {
		return nil, false, errors.New("helm requester not configured")
	}
	status, err := helm.Status(ctx, clusterID, releaseName, namespace)
	if err == nil {
		return status, true, nil
	}
	if isHelmReleaseNotFound(err) {
		return nil, false, nil
	}
	return nil, false, err
}

func adoptExistingToolRelease(ctx context.Context, queries toolInstallPersister, clusterID uuid.UUID, env toolOperationEnvelope, status *protocol.HelmResultPayload) error {
	if queries == nil {
		return errors.New("tool queries not configured")
	}
	if status == nil {
		return errors.New("helm status not provided")
	}
	preset := pgtype.Text{String: env.Preset, Valid: env.Preset != ""}
	toolSlug := pgtype.Text{String: env.ToolSlug, Valid: env.ToolSlug != ""}
	params := sqlc.GetInstalledChartByReleaseParams{
		ClusterID:   clusterID,
		ReleaseName: env.ReleaseName,
		Namespace:   env.Namespace,
	}
	if _, err := queries.GetInstalledChartByRelease(ctx, params); err == nil {
		_, err = queries.AdoptInstalledChartByRelease(ctx, sqlc.AdoptInstalledChartByReleaseParams{
			ClusterID:      clusterID,
			ReleaseName:    env.ReleaseName,
			Namespace:      env.Namespace,
			ToolSlug:       toolSlug,
			PresetUsed:     preset,
			ValuesOverride: env.ValuesYAML,
			Status:         normalizeToolStatus(status.Status),
			Revision:       int32(status.Revision),
		})
		return err
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err := queries.CreateInstalledChart(ctx, sqlc.CreateInstalledChartParams{
		ClusterID:      clusterID,
		ReleaseName:    env.ReleaseName,
		Namespace:      env.Namespace,
		ValuesOverride: env.ValuesYAML,
		Status:         normalizeToolStatus(status.Status),
		Revision:       int32(status.Revision),
		ToolSlug:       toolSlug,
		PresetUsed:     preset,
	})
	return err
}

func isHelmReleaseNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "release: not found") || strings.Contains(msg, "release not found")
}

func operationTargetKey(clusterID uuid.UUID, slug string) string {
	return clusterID.String() + ":" + slug
}

func (h *ToolHandler) recordToolOperationEvent(ctx context.Context, operationID uuid.UUID, level, stage, message string, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, _ = h.queries.CreateToolOperationEvent(ctx, sqlc.CreateToolOperationEventParams{
		OperationID: operationID,
		Level:       level,
		Stage:       stage,
		Message:     message,
		Detail:      raw,
	})
}

// Phase B5 — CIS operator
//
// Catalog entry for `rancher/cis-operator`. Per-cluster install. Once the
// release is rolled out, the operator surfaces three CRDs that astronomer-go
// proxies through the existing tunnel:
//
//   - `ClusterScan`         (cis.cattle.io/v1) — created by SecurityHandler.CreateScan
//   - `ClusterScanProfile`  (cis.cattle.io/v1) — listed by SecurityHandler.ListProfiles
//   - `ClusterScanReport`   (cis.cattle.io/v1) — polled by tasks.HandleSecurityIngest
//
// The actual `cluster_tools` row is seeded in migration
// `022_cis_scan_findings.up.sql` (idempotent INSERT … ON CONFLICT DO
// NOTHING) so the catalog is consistent across fresh deploys, replicas, and
// existing installs without requiring a separate seed step. Keeping the
// chart coordinates here as a Go-side constant means handlers and tasks can
// reference them without re-querying the DB.
const (
	CISOperatorToolSlug      = "cis-operator"
	CISOperatorToolName      = "CIS Scanner (Rancher)"
	CISOperatorChartName     = "rancher-cis-benchmark"
	CISOperatorChartRepoURL  = "https://charts.rancher.io"
	CISOperatorChartCategory = "security"
	CISOperatorNamespace     = "cis-operator-system"
)

// CISOperatorChartCoordinates returns the chart coordinates for cis-operator
// as the same struct shape stored under `cluster_tools.charts`. Exposed so
// tests and other packages can reference one source of truth.
func CISOperatorChartCoordinates() toolChart {
	return toolChart{
		ChartName: CISOperatorChartName,
		RepoURL:   CISOperatorChartRepoURL,
		Namespace: CISOperatorNamespace,
		Order:     0,
	}
}

// Phase B1 — ArgoCD lifecycle
//
// Catalog entry for `argo/argo-cd`. Per-cluster install. Once the release is
// rolled out, the chart provides the argocd-server, argocd-repo-server,
// argocd-application-controller, and argocd-applicationset-controller
// workloads. Astronomer registers the resulting instance via
// POST /api/v1/argocd/instances/ and from then on uses the typed client in
// internal/handler/argocd to drive Application/AppProject/ApplicationSet/
// Cluster/Repository CRUD against it.
//
// The `cluster_tools` row is seeded by an idempotent INSERT in the same
// migration that adds the argocd_managed_clusters table. Keeping the chart
// coordinates here as Go constants means handlers and follow-up auto-
// registration code can reference them without re-querying the DB.
const (
	ArgoCDToolSlug      = "argocd"
	ArgoCDToolName      = "ArgoCD"
	ArgoCDChartName     = "argo-cd"
	ArgoCDChartRepoURL  = "https://argoproj.github.io/argo-helm"
	ArgoCDChartCategory = "gitops"
	ArgoCDNamespace     = "argocd"
)

// ArgoCDChartCoordinates returns the chart coordinates for argo-cd as the
// same struct shape stored under `cluster_tools.charts`. Exposed so tests
// and other packages can reference one source of truth.
func ArgoCDChartCoordinates() toolChart {
	return toolChart{
		ChartName: ArgoCDChartName,
		RepoURL:   ArgoCDChartRepoURL,
		Namespace: ArgoCDNamespace,
		Order:     0,
	}
}

// ArgoCDDefaultValuesYAML is the conservative single-node default values
// snippet baked into the catalog `presets["default"]`. It targets:
//   - server: ClusterIP service, ingress disabled, rootpath /argocd so the
//     SPA + API both live under that prefix and the Astronomer reverse
//     proxy can mount it transparently.
//   - controller: HA off (replicas=1) — flip to 2+ later for prod.
//   - redis-ha: off — chart's bundled redis is sufficient for day-one.
//   - applicationSet: enabled — required for Phase B1's fan-out endpoints.
//   - dex: disabled — Astronomer's Dex shim brokers identity instead.
//   - configs.cm.accounts.astronomer = "apiKey, login": dedicated upstream
//     account that lets us mint NEVER-EXPIRING API tokens. Without this the
//     only path is admin's session JWT which expires after 24h and silently
//     flips the instance to "unhealthy" until someone re-mints it.
//   - configs.rbac.policy.csv: bind that account to the upstream `admin`
//     role so it can drive ApplicationSet / cluster / project CRUD.
//
// The values are kept as a Go literal so tests can assert the shape; the
// migration seeds the same string into `cluster_tools.presets`.
const ArgoCDDefaultValuesYAML = `server:
  service:
    type: ClusterIP
  ingress:
    enabled: false
controller:
  replicas: 1
redis-ha:
  enabled: false
applicationSet:
  enabled: true
dex:
  enabled: false
configs:
  params:
    server.insecure: "true"
    server.rootpath: "/argocd"
    server.basehref: "/argocd"
  cm:
    accounts.astronomer: "apiKey, login"
  rbac:
    policy.default: "role:readonly"
    policy.csv: |
      g, astronomer, role:admin
`

// Phase B4 — Dex
//
// Catalog entry for `dex` from the dexidp Helm repo. Single-instance install
// in the management cluster; we don't run Dex per-tenant. Once the release is
// rolled out, the operator's flow is:
//
//  1. PUT /api/v1/auth/dex/settings/ with the public issuer URL Astronomer
//     should treat as the OIDC provider (e.g. https://dex.example.com).
//  2. POST /api/v1/auth/dex/connectors/ for each upstream IdP (Azure AD,
//     LDAP, SAML, ...).
//  3. POST /api/v1/auth/dex/apply/ to render + write the ConfigMap that the
//     chart's deployment mounts. Dex hot-reloads; no pod restart needed.
//  4. POST /api/v1/auth/dex/register-as-sso/ to add a `dex` row in
//     sso_configurations so A1's generic OIDC path treats Dex as a regular
//     /auth/login/dex/ provider.
//
// The actual `cluster_tools` row is seeded in migration
// `023_dex_connectors.up.sql` (idempotent INSERT … ON CONFLICT DO NOTHING)
// alongside the new dex_connectors / dex_settings tables. Chart coordinates
// stay here as Go constants so handlers/tests have one source of truth.
const (
	DexToolSlug         = "dex"
	DexToolName         = "Dex Identity Broker"
	DexChartName        = "dex"
	DexChartRepoURL     = "https://charts.dexidp.io"
	DexChartCategory    = "auth"
	DexDefaultNamespace = "dex"
	DexConfigMapName    = "astronomer-dex-config"
)

// DexChartCoordinates returns the chart coordinates for dex as the same
// struct shape stored under `cluster_tools.charts`.
func DexChartCoordinates() toolChart {
	return toolChart{
		ChartName: DexChartName,
		RepoURL:   DexChartRepoURL,
		Namespace: DexDefaultNamespace,
		Order:     0,
	}
}

// Phase B2 — Velero backup engine
//
// Catalog entry for `velero` from the vmware-tanzu Helm repo. Per-cluster
// install — Velero must run in each cluster you want to back up. Once the
// release is rolled out, the operator's flow is:
//
//  1. POST /api/v1/backups/storage/ with the cloud credentials + cluster id;
//     BackupHandler.applyVeleroBSL writes the BackupStorageLocation CR and
//     a credentials Secret into the Velero namespace.
//  2. POST /api/v1/backups/schedules/ with cron + include/exclude filters;
//     BackupHandler.applyVeleroSchedule projects the row into a Velero
//     `Schedule` CR. Velero's controller fans backups out on cron upstream.
//  3. POST /api/v1/backups/schedules/{id}/trigger-now/ creates a one-off
//     Velero `Backup` CR for instant backups; the reconciler polls
//     `status.phase` until terminal.
//  4. POST /api/v1/backups/{id}/restore/ writes a Velero `Restore` CR.
//
// The chart's BSL/VSL are intentionally empty in the default values: the
// real BackupStorageLocation is created from the BackupStorageConfig handler
// once a user wires up their cloud destination — keeping the install path
// independent of credentials so we can install Velero before anyone has
// configured S3.
//
// The cluster_tools row is seeded by an idempotent INSERT in migration
// 020_velero_backup_engine.up.sql alongside the schema changes that track
// the Velero CR identities. Chart coordinates stay here as Go constants so
// handlers and tests share one source of truth.
const (
	VeleroToolSlug         = "velero"
	VeleroToolName         = "Velero"
	VeleroChartName        = "velero"
	VeleroChartRepoURL     = "https://vmware-tanzu.github.io/helm-charts"
	VeleroChartCategory    = "backup"
	VeleroDefaultNamespace = "velero"
)

// VeleroChartCoordinates returns the chart coordinates for velero as the same
// struct shape stored under `cluster_tools.charts`.
func VeleroChartCoordinates() toolChart {
	return toolChart{
		ChartName: VeleroChartName,
		RepoURL:   VeleroChartRepoURL,
		Namespace: VeleroDefaultNamespace,
		Order:     0,
	}
}

// Supportability / TLS posture — cert-manager
//
// Catalog entry for `cert-manager` from the Jetstack Helm repo. It can run on
// either the management cluster or workload clusters; Astronomer uses it
// primarily to automate TLS for the management Gateway, but operators may also
// use it for app/workload ingress on managed clusters.
//
// The actual `cluster_tools` row is seeded in migration
// `033_cert_manager_tool.up.sql` (idempotent INSERT … ON CONFLICT DO NOTHING)
// so existing installs gain the entry on upgrade.
const (
	CertManagerToolSlug         = "cert-manager"
	CertManagerToolName         = "cert-manager"
	CertManagerChartName        = "cert-manager"
	CertManagerChartRepoURL     = "https://charts.jetstack.io"
	CertManagerChartCategory    = "security"
	CertManagerDefaultNamespace = "cert-manager"
)

// CertManagerChartCoordinates returns the chart coordinates for cert-manager
// as the same struct shape stored under `cluster_tools.charts`.
func CertManagerChartCoordinates() toolChart {
	return toolChart{
		ChartName: CertManagerChartName,
		RepoURL:   CertManagerChartRepoURL,
		Namespace: CertManagerDefaultNamespace,
		Order:     0,
	}
}

// VeleroDefaultValuesYAML is the conservative defaults snippet baked into the
// catalog `presets["default"]`. We intentionally leave both backupStorageLocation
// and volumeSnapshotLocation empty — the BackupStorageLocation is owned by
// BackupHandler and is populated when the user POSTs to /backups/storage/.
//
// `initContainers` enumerates the provider plugins available for selection in
// the UI; users pick one (aws / gcp / azure / csi) at install time and we
// emit only the chosen plugin into the rendered values.
const VeleroDefaultValuesYAML = `configuration:
  backupStorageLocation: []
  volumeSnapshotLocation: []
deployNodeAgent: false
snapshotsEnabled: true
backupsEnabled: true
metrics:
  enabled: true
serviceAccount:
  server:
    create: true
initContainers: []
`

// ToolScope describes where a catalog tool is meant to run.
type ToolScope string

const (
	// ToolScopeControlPlane means the tool is part of the management plane
	// and should only be installed on the local Astronomer cluster (Dex,
	// ArgoCD). Installing it on a workload cluster is almost always a
	// mistake — nothing on the workload cluster consumes it.
	ToolScopeControlPlane ToolScope = "control-plane"
	// ToolScopeWorkload means the tool is data-plane: it runs on each
	// workload cluster (Velero, CIS scanner, metrics-server). Installing it
	// on the management cluster is fine but rarely useful.
	ToolScopeWorkload ToolScope = "workload"
	// ToolScopeAny means there's no opinion — any cluster is acceptable.
	ToolScopeAny ToolScope = "any"
)

// toolScopes is the catalog→scope map. New tools should be added here so the
// install path can refuse misuse early. When a slug isn't listed we default
// to ToolScopeAny rather than blocking, which keeps the catalog extensible
// without requiring every tool to be classified.
var toolScopes = map[string]ToolScope{
	DexToolSlug:         ToolScopeControlPlane,
	ArgoCDToolSlug:      ToolScopeControlPlane,
	VeleroToolSlug:      ToolScopeWorkload,
	CISOperatorToolSlug: ToolScopeWorkload,
}

// scopeForTool returns the configured scope for a tool slug, falling back to
// ToolScopeAny when no policy is registered.
func scopeForTool(slug string) ToolScope {
	if s, ok := toolScopes[slug]; ok {
		return s
	}
	return ToolScopeAny
}

// checkToolScope enforces the control-plane vs workload policy at install
// time. Returns (errorMessage, ok=false) when the cluster type is wrong for
// the tool's scope; (_, ok=true) means the install can proceed. A DB lookup
// failure is treated as ok=true so we never accidentally hard-block valid
// installs on a transient infrastructure issue — the actual install would
// fail loudly enough on its own.
func (h *ToolHandler) checkToolScope(ctx context.Context, slug string, clusterID uuid.UUID) (string, bool) {
	scope := scopeForTool(slug)
	if scope == ToolScopeAny {
		return "", true
	}
	cluster, err := h.queries.GetClusterByID(ctx, clusterID)
	if err != nil {
		return "", true
	}
	switch scope {
	case ToolScopeControlPlane:
		if !cluster.IsLocal {
			return fmt.Sprintf("%s is a control-plane tool and can only be installed on the local Astronomer cluster", slug), false
		}
	case ToolScopeWorkload:
		if cluster.IsLocal {
			return fmt.Sprintf("%s is a workload-cluster tool and should not be installed on the local Astronomer cluster", slug), false
		}
	}
	return "", true
}
