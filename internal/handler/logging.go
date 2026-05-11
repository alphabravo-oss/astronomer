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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// LoggingNamespace is the namespace on the managed cluster where rendered
// Fluent Bit / output ConfigMaps live. We assume Fluent Bit is already
// installed and watching this namespace; installing it is out of scope for
// this controller (it's the agent's / platform-bootstrap's job).
const LoggingNamespace = "astronomer-logging"

// loggingReconcileInterval is how often the background loop sweeps the
// pending-operations queue. Matches the catalog/tools cadence so operators
// see consistent reconcile timing across subsystems.
const loggingReconcileInterval = 30 * time.Second

// LoggingQuerier abstracts the logging-related database queries needed by LoggingHandler.
type LoggingQuerier interface {
	// Outputs
	ListLoggingOutputs(ctx context.Context, arg sqlc.ListLoggingOutputsParams) ([]sqlc.LoggingOutput, error)
	ListOutputsByCluster(ctx context.Context, arg sqlc.ListOutputsByClusterParams) ([]sqlc.LoggingOutput, error)
	GetLoggingOutputByID(ctx context.Context, id uuid.UUID) (sqlc.LoggingOutput, error)
	CreateLoggingOutput(ctx context.Context, arg sqlc.CreateLoggingOutputParams) (sqlc.LoggingOutput, error)
	UpdateLoggingOutput(ctx context.Context, arg sqlc.UpdateLoggingOutputParams) (sqlc.LoggingOutput, error)
	DeleteLoggingOutput(ctx context.Context, id uuid.UUID) error
	CountLoggingOutputs(ctx context.Context) (int64, error)
	// Pipelines
	ListLoggingPipelines(ctx context.Context, arg sqlc.ListLoggingPipelinesParams) ([]sqlc.LoggingPipeline, error)
	ListPipelinesByCluster(ctx context.Context, arg sqlc.ListPipelinesByClusterParams) ([]sqlc.LoggingPipeline, error)
	GetLoggingPipelineByID(ctx context.Context, id uuid.UUID) (sqlc.LoggingPipeline, error)
	CreateLoggingPipeline(ctx context.Context, arg sqlc.CreateLoggingPipelineParams) (sqlc.LoggingPipeline, error)
	UpdateLoggingPipeline(ctx context.Context, arg sqlc.UpdateLoggingPipelineParams) (sqlc.LoggingPipeline, error)
	DeleteLoggingPipeline(ctx context.Context, id uuid.UUID) error
	CountLoggingPipelines(ctx context.Context) (int64, error)
	// Operations
	CreateLoggingOperation(ctx context.Context, arg sqlc.CreateLoggingOperationParams) (sqlc.LoggingOperation, error)
	GetLoggingOperation(ctx context.Context, id uuid.UUID) (sqlc.LoggingOperation, error)
	ListLoggingOperations(ctx context.Context, arg sqlc.ListLoggingOperationsParams) ([]sqlc.LoggingOperation, error)
	ListPendingLoggingOperations(ctx context.Context, limit int32) ([]sqlc.LoggingOperation, error)
	MarkLoggingOperationRunning(ctx context.Context, id uuid.UUID) (sqlc.LoggingOperation, error)
	MarkLoggingOperationCompleted(ctx context.Context, id uuid.UUID) (sqlc.LoggingOperation, error)
	MarkLoggingOperationFailed(ctx context.Context, arg sqlc.MarkLoggingOperationFailedParams) (sqlc.LoggingOperation, error)
	MarkLoggingOperationSuperseded(ctx context.Context, arg sqlc.MarkLoggingOperationSupersededParams) (sqlc.LoggingOperation, error)
	RequeueLoggingOperation(ctx context.Context, id uuid.UUID) (sqlc.LoggingOperation, error)
	CreateLoggingOperationEvent(ctx context.Context, arg sqlc.CreateLoggingOperationEventParams) (sqlc.LoggingOperationEvent, error)
	ListLoggingOperationEvents(ctx context.Context, operationID uuid.UUID) ([]sqlc.LoggingOperationEvent, error)
}

// LoggingHandler handles logging output and pipeline endpoints.
//
// As of the logging-controller refactor (comparison.md §7/§10/§11) the
// handler no longer applies to the cluster inline: it writes intent rows to
// the `logging_operations` table and a background reconciler picks them up.
// The reconciler renders a tiny ConfigMap for each output/pipeline and
// applies it via the tunnel K8sRequester into the LoggingNamespace on the
// target managed cluster, where Fluent Bit (assumed already installed)
// watches.
type LoggingHandler struct {
	queries   LoggingQuerier
	requester K8sRequester
	log       *slog.Logger
	authz     authorizationSupport
	mu        sync.Mutex
	trigger   chan struct{}
}

// NewLoggingHandler creates a new logging handler.
func NewLoggingHandler(queries LoggingQuerier) *LoggingHandler {
	return &LoggingHandler{
		queries: queries,
		log:     slog.Default(),
		trigger: make(chan struct{}, 1),
	}
}

// SetK8sRequester wires the tunnel-backed K8sRequester the reconciler uses
// to apply ConfigMaps into managed clusters. Without it the reconciler still
// runs but fails apply operations with a clear error.
func (h *LoggingHandler) SetK8sRequester(r K8sRequester) {
	if h == nil {
		return
	}
	h.requester = r
}

// SetLogger wires a structured logger.
func (h *LoggingHandler) SetLogger(log *slog.Logger) {
	if h == nil || log == nil {
		return
	}
	h.log = log
}

// SetAuthorization wires per-cluster RBAC for the operations endpoints.
// Matches the catalog/tools/argocd pattern so the same engine + querier
// instances are shared across handlers.
func (h *LoggingHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	if h == nil {
		return
	}
	h.authz.SetAuthorization(engine, querier)
}

// StartReconciler launches the background loop that processes pending logging
// operations. Safe to call before SetK8sRequester — the reconciler will
// surface a "tunnel not configured" error per attempted apply until wired.
func (h *LoggingHandler) StartReconciler(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	go h.runReconciler(ctx)
}

// TriggerReconcile nudges the reconciler so newly-enqueued operations don't
// wait for the next tick. Non-blocking; if a wakeup is already pending it
// silently drops.
func (h *LoggingHandler) TriggerReconcile() {
	if h == nil || h.trigger == nil {
		return
	}
	select {
	case h.trigger <- struct{}{}:
	default:
	}
}

func (h *LoggingHandler) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(loggingReconcileInterval)
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

// ControllerStatus summarizes logging subsystem configuration state.
func (h *LoggingHandler) ControllerStatus(w http.ResponseWriter, r *http.Request) {
	summary, err := h.controllerSummary(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "status_error", "Failed to load logging outputs")
		return
	}
	RespondJSON(w, http.StatusOK, summary)
}

func (h *LoggingHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	outputs, err := h.queries.ListLoggingOutputs(ctx, sqlc.ListLoggingOutputsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	pipelines, err := h.queries.ListLoggingPipelines(ctx, sqlc.ListLoggingPipelinesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	outputTypes := map[string]int{}
	clusterOutputs := map[string]int{}
	enabledOutputs := 0
	for _, output := range outputs {
		outputTypes[output.OutputType]++
		if output.Enabled {
			enabledOutputs++
		}
		if output.ClusterID.Valid {
			clusterOutputs[uuid.UUID(output.ClusterID.Bytes).String()]++
		}
	}
	clusterPipelines := map[string]int{}
	enabledPipelines := 0
	for _, pipeline := range pipelines {
		if pipeline.Enabled {
			enabledPipelines++
		}
		clusterPipelines[pipeline.ClusterID.String()]++
	}
	health := "healthy"
	reasons := make([]string, 0, 2)
	if enabledPipelines > 0 && enabledOutputs == 0 {
		health = "degraded"
		reasons = append(reasons, "enabled_pipelines_without_outputs")
	}

	// Aggregate operation status. This is the "most-recent-per-target"
	// rollup the spec asks for: it gives operators a queue-depth signal and
	// surfaces recent failures in the controller status panel.
	ops, _ := h.queries.ListLoggingOperations(ctx, sqlc.ListLoggingOperationsParams{Limit: 500, Offset: 0})
	opCounts := map[string]int{}
	staleRunning := 0
	recentFailures := 0
	var latestFailure map[string]any
	recent := make([]map[string]any, 0, 5)
	seenTarget := map[string]bool{}
	mostRecentPerTarget := map[string]string{} // target -> status
	for _, op := range ops {
		opCounts[op.Status]++
		if op.Status == "running" && op.StartedAt.Valid && time.Since(op.StartedAt.Time) > time.Minute {
			staleRunning++
		}
		key := op.TargetType + ":" + op.TargetKey
		if !seenTarget[key] {
			seenTarget[key] = true
			mostRecentPerTarget[key] = op.Status
		}
		if len(recent) < 5 {
			recent = append(recent, loggingOperationResponse(op))
		}
		if op.Status == "failed" && time.Since(op.CreatedAt) <= 30*time.Minute {
			recentFailures++
		}
		if latestFailure == nil && op.Status == "failed" {
			latestFailure = loggingOperationResponse(op)
		}
	}
	return map[string]any{
		"reconciler": map[string]any{
			"enabled":              true,
			"queueDepth":           opCounts["pending"] + opCounts["running"],
			"staleRunningCount":    staleRunning,
			"staleThresholdSecond": 60,
		},
		"health":        health,
		"healthReasons": reasons,
		"outputs": map[string]any{
			"total":              len(outputs),
			"enabledCount":       enabledOutputs,
			"types":              outputTypes,
			"configuredClusters": len(clusterOutputs),
		},
		"pipelines": map[string]any{
			"total":              len(pipelines),
			"enabledCount":       enabledPipelines,
			"configuredClusters": len(clusterPipelines),
		},
		"operations":         opCounts,
		"recentFailureCount": recentFailures,
		"recentOperations":   recent,
		"latestFailure":      latestFailure,
	}, nil
}

// --- Request types ---

// CreateLoggingOutputRequest represents the request body for creating a logging output.
//
// ClusterID is accepted at the top level of the body for parity with the
// Next.js frontend, which posts to /api/v1/logging/outputs/ with the cluster
// ID in the body rather than the URL. Query (?cluster_id=) is preferred when
// present; otherwise we fall back to this body field.
type CreateLoggingOutputRequest struct {
	Name          string          `json:"name"`
	OutputType    string          `json:"output_type"`
	Configuration json.RawMessage `json:"configuration"`
	ClusterID     string          `json:"cluster_id"`
	Enabled       bool            `json:"enabled"`
}

// CreateLoggingPipelineRequest represents the request body for creating a logging pipeline.
type CreateLoggingPipelineRequest struct {
	Name       string          `json:"name"`
	Namespaces json.RawMessage `json:"namespaces"`
	Labels     json.RawMessage `json:"labels"`
	Filters    json.RawMessage `json:"filters"`
	Enabled    bool            `json:"enabled"`
}

// --- Output endpoints ---

// ListOutputs handles GET /api/v1/clusters/{cluster_id}/logging/outputs/.
func (h *LoggingHandler) ListOutputs(w http.ResponseWriter, r *http.Request) {
	clusterID, err := clusterIDFromRequest(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	outputs, err := h.queries.ListOutputsByCluster(r.Context(), sqlc.ListOutputsByClusterParams{
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true},
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list logging outputs")
		return
	}

	total, err := h.queries.CountLoggingOutputs(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count logging outputs")
		return
	}

	RespondPaginated(w, r, outputs, total)
}

// CreateOutput handles POST /api/v1/clusters/{cluster_id}/logging/outputs/.
func (h *LoggingHandler) CreateOutput(w http.ResponseWriter, r *http.Request) {
	var req CreateLoggingOutputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	// Prefer URL/query cluster_id; fall back to the top-level body field, then
	// the legacy configuration.cluster_id nested form.
	clusterID, err := clusterIDFromRequest(r)
	if err != nil {
		if req.ClusterID != "" {
			clusterID, err = uuid.Parse(req.ClusterID)
		}
		if err != nil {
			clusterID, err = clusterIDFromRequestOrBody(r, req.Configuration)
		}
	}
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Output name is required")
		return
	}

	if req.OutputType == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Output type is required")
		return
	}

	configuration := req.Configuration
	if configuration == nil {
		configuration = json.RawMessage(`{}`)
	}

	output, err := h.queries.CreateLoggingOutput(r.Context(), sqlc.CreateLoggingOutputParams{
		Name:          req.Name,
		OutputType:    req.OutputType,
		Configuration: configuration,
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Enabled:       req.Enabled,
		CreatedByID:   currentUserUUID(r),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create logging output")
		return
	}

	op, opErr := h.enqueueOutputApply(r.Context(), output, currentUserUUID(r))
	if opErr != nil && h.log != nil {
		h.log.Warn("logging: failed to enqueue output apply", "id", output.ID.String(), "error", opErr)
	}

	recordAudit(r, h.queries, "logging.output.create", "logging_output", output.ID.String(), output.Name, map[string]any{
		"cluster_id":   clusterID.String(),
		"output_type":  output.OutputType,
		"enabled":      output.Enabled,
		"operation_id": operationIDOrEmpty(op),
	})

	RespondJSON(w, http.StatusCreated, output)
}

// UpdateOutput handles PUT /api/v1/logging/outputs/{id}/.
func (h *LoggingHandler) UpdateOutput(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid output ID")
		return
	}
	var req CreateLoggingOutputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	output, err := h.queries.UpdateLoggingOutput(r.Context(), sqlc.UpdateLoggingOutputParams{
		ID:            id,
		Name:          req.Name,
		OutputType:    req.OutputType,
		Configuration: req.Configuration,
		Enabled:       req.Enabled,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update logging output")
		return
	}
	op, opErr := h.enqueueOutputApply(r.Context(), output, currentUserUUID(r))
	if opErr != nil && h.log != nil {
		h.log.Warn("logging: failed to enqueue output apply", "id", output.ID.String(), "error", opErr)
	}
	recordAudit(r, h.queries, "logging.output.update", "logging_output", output.ID.String(), output.Name, map[string]any{
		"output_type":  output.OutputType,
		"enabled":      output.Enabled,
		"operation_id": operationIDOrEmpty(op),
	})
	RespondJSON(w, http.StatusOK, output)
}

// TestOutput handles POST /api/v1/logging/outputs/{id}/test/.
//
// Previously returned 501. Now triggers an apply operation, which proxies
// the configuration through the agent — operators get a real round-trip
// signal instead of "not implemented".
func (h *LoggingHandler) TestOutput(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid output ID")
		return
	}
	output, err := h.queries.GetLoggingOutputByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Logging output not found")
		return
	}
	op, err := h.enqueueOutputApply(r.Context(), output, currentUserUUID(r))
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "enqueue_error", "Failed to enqueue apply test")
		return
	}
	RespondJSON(w, http.StatusAccepted, map[string]any{
		"success":   true,
		"message":   "Logging output apply enqueued",
		"operation": loggingOperationResponse(op),
	})
}

// DeleteOutput handles DELETE /api/v1/clusters/{cluster_id}/logging/outputs/{id}/.
func (h *LoggingHandler) DeleteOutput(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid output ID")
		return
	}

	existing, lookupErr := h.queries.GetLoggingOutputByID(r.Context(), id)
	outputName := ""
	if lookupErr == nil {
		outputName = existing.Name
	}
	// Enqueue the delete operation BEFORE the row goes away — the
	// reconciler will use the snapshot in the payload to know which
	// ConfigMap to remove.
	var deleteOp sqlc.LoggingOperation
	if lookupErr == nil {
		op, opErr := h.enqueueOutputDelete(r.Context(), existing, currentUserUUID(r))
		if opErr != nil && h.log != nil {
			h.log.Warn("logging: failed to enqueue output delete", "id", id.String(), "error", opErr)
		}
		deleteOp = op
	}

	if err := h.queries.DeleteLoggingOutput(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Logging output not found")
		return
	}

	recordAudit(r, h.queries, "logging.output.delete", "logging_output", id.String(), outputName, map[string]any{
		"operation_id": operationIDOrEmpty(deleteOp),
	})

	w.WriteHeader(http.StatusNoContent)
}

// --- Pipeline endpoints ---

// ListPipelines handles GET /api/v1/clusters/{cluster_id}/logging/pipelines/.
func (h *LoggingHandler) ListPipelines(w http.ResponseWriter, r *http.Request) {
	clusterID, err := clusterIDFromRequest(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	pipelines, err := h.queries.ListPipelinesByCluster(r.Context(), sqlc.ListPipelinesByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list logging pipelines")
		return
	}

	total, err := h.queries.CountLoggingPipelines(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count logging pipelines")
		return
	}

	RespondPaginated(w, r, pipelines, total)
}

// CreatePipeline handles POST /api/v1/clusters/{cluster_id}/logging/pipelines/.
func (h *LoggingHandler) CreatePipeline(w http.ResponseWriter, r *http.Request) {
	var req CreateLoggingPipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	clusterID, err := clusterIDFromRequest(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Pipeline name is required")
		return
	}

	namespaces := req.Namespaces
	if namespaces == nil {
		namespaces = json.RawMessage(`[]`)
	}
	labels := req.Labels
	if labels == nil {
		labels = json.RawMessage(`{}`)
	}
	filters := req.Filters
	if filters == nil {
		filters = json.RawMessage(`{}`)
	}

	pipeline, err := h.queries.CreateLoggingPipeline(r.Context(), sqlc.CreateLoggingPipelineParams{
		Name:        req.Name,
		ClusterID:   clusterID,
		Namespaces:  namespaces,
		Labels:      labels,
		Filters:     filters,
		Enabled:     req.Enabled,
		CreatedByID: currentUserUUID(r),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create logging pipeline")
		return
	}

	op, opErr := h.enqueuePipelineApply(r.Context(), pipeline, currentUserUUID(r))
	if opErr != nil && h.log != nil {
		h.log.Warn("logging: failed to enqueue pipeline apply", "id", pipeline.ID.String(), "error", opErr)
	}

	recordAudit(r, h.queries, "logging.pipeline.create", "logging_pipeline", pipeline.ID.String(), pipeline.Name, map[string]any{
		"cluster_id":   clusterID.String(),
		"enabled":      pipeline.Enabled,
		"operation_id": operationIDOrEmpty(op),
	})

	RespondJSON(w, http.StatusCreated, pipeline)
}

// UpdatePipeline handles PUT /api/v1/logging/pipelines/{id}/.
func (h *LoggingHandler) UpdatePipeline(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid pipeline ID")
		return
	}
	var req CreateLoggingPipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	pipeline, err := h.queries.UpdateLoggingPipeline(r.Context(), sqlc.UpdateLoggingPipelineParams{
		ID:         id,
		Name:       req.Name,
		Namespaces: req.Namespaces,
		Labels:     req.Labels,
		Filters:    req.Filters,
		Enabled:    req.Enabled,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update logging pipeline")
		return
	}
	op, opErr := h.enqueuePipelineApply(r.Context(), pipeline, currentUserUUID(r))
	if opErr != nil && h.log != nil {
		h.log.Warn("logging: failed to enqueue pipeline apply", "id", pipeline.ID.String(), "error", opErr)
	}
	recordAudit(r, h.queries, "logging.pipeline.update", "logging_pipeline", pipeline.ID.String(), pipeline.Name, map[string]any{
		"enabled":      pipeline.Enabled,
		"operation_id": operationIDOrEmpty(op),
	})
	RespondJSON(w, http.StatusOK, pipeline)
}

// DeletePipeline handles DELETE /api/v1/clusters/{cluster_id}/logging/pipelines/{id}/.
func (h *LoggingHandler) DeletePipeline(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid pipeline ID")
		return
	}

	existing, lookupErr := h.queries.GetLoggingPipelineByID(r.Context(), id)
	pipelineName := ""
	if lookupErr == nil {
		pipelineName = existing.Name
	}
	var deleteOp sqlc.LoggingOperation
	if lookupErr == nil {
		op, opErr := h.enqueuePipelineDelete(r.Context(), existing, currentUserUUID(r))
		if opErr != nil && h.log != nil {
			h.log.Warn("logging: failed to enqueue pipeline delete", "id", id.String(), "error", opErr)
		}
		deleteOp = op
	}

	if err := h.queries.DeleteLoggingPipeline(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Logging pipeline not found")
		return
	}

	recordAudit(r, h.queries, "logging.pipeline.delete", "logging_pipeline", id.String(), pipelineName, map[string]any{
		"operation_id": operationIDOrEmpty(deleteOp),
	})

	w.WriteHeader(http.StatusNoContent)
}

// EnableOutput handles POST /api/v1/logging/outputs/{id}/enable/.
func (h *LoggingHandler) EnableOutput(w http.ResponseWriter, r *http.Request) {
	h.setOutputEnabled(w, r, true)
}

// DisableOutput handles POST /api/v1/logging/outputs/{id}/disable/.
func (h *LoggingHandler) DisableOutput(w http.ResponseWriter, r *http.Request) {
	h.setOutputEnabled(w, r, false)
}

func (h *LoggingHandler) setOutputEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid output ID")
		return
	}
	current, err := h.queries.GetLoggingOutputByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Logging output not found")
		return
	}
	output, err := h.queries.UpdateLoggingOutput(r.Context(), sqlc.UpdateLoggingOutputParams{
		ID:            id,
		Name:          current.Name,
		OutputType:    current.OutputType,
		Configuration: current.Configuration,
		Enabled:       enabled,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update logging output")
		return
	}
	// Re-render the ConfigMap on enable/disable so cluster state tracks
	// intent. When disabled we still apply — the rendered config will
	// reflect enabled=false so Fluent Bit can skip it.
	op, opErr := h.enqueueOutputApply(r.Context(), output, currentUserUUID(r))
	if opErr != nil && h.log != nil {
		h.log.Warn("logging: failed to enqueue output apply", "id", output.ID.String(), "error", opErr)
	}
	action := "logging.output.enable"
	if !enabled {
		action = "logging.output.disable"
	}
	recordAudit(r, h.queries, action, "logging_output", output.ID.String(), output.Name, map[string]any{
		"enabled":      enabled,
		"operation_id": operationIDOrEmpty(op),
	})
	RespondJSON(w, http.StatusOK, output)
}

// QueryOutput handles POST /api/v1/logging/outputs/{id}/query/.
// Returns 501 because querying log backends requires per-backend client wiring.
func (h *LoggingHandler) QueryOutput(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid output ID")
		return
	}
	if _, err := h.queries.GetLoggingOutputByID(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Logging output not found")
		return
	}
	// TODO: Implement per-backend log query (Loki/ES/etc) once a query client
	// is wired through. For now respond with a clear not-implemented status.
	RespondError(w, http.StatusNotImplemented, "not_implemented", "Querying logs from this output backend is not yet implemented")
}

// EnablePipeline handles POST /api/v1/logging/pipelines/{id}/enable/.
func (h *LoggingHandler) EnablePipeline(w http.ResponseWriter, r *http.Request) {
	h.setPipelineEnabled(w, r, true)
}

// DisablePipeline handles POST /api/v1/logging/pipelines/{id}/disable/.
func (h *LoggingHandler) DisablePipeline(w http.ResponseWriter, r *http.Request) {
	h.setPipelineEnabled(w, r, false)
}

func (h *LoggingHandler) setPipelineEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid pipeline ID")
		return
	}
	current, err := h.queries.GetLoggingPipelineByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Logging pipeline not found")
		return
	}
	pipeline, err := h.queries.UpdateLoggingPipeline(r.Context(), sqlc.UpdateLoggingPipelineParams{
		ID:         id,
		Name:       current.Name,
		Namespaces: current.Namespaces,
		Labels:     current.Labels,
		Filters:    current.Filters,
		Enabled:    enabled,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update logging pipeline")
		return
	}
	op, opErr := h.enqueuePipelineApply(r.Context(), pipeline, currentUserUUID(r))
	if opErr != nil && h.log != nil {
		h.log.Warn("logging: failed to enqueue pipeline apply", "id", pipeline.ID.String(), "error", opErr)
	}
	action := "logging.pipeline.enable"
	if !enabled {
		action = "logging.pipeline.disable"
	}
	recordAudit(r, h.queries, action, "logging_pipeline", pipeline.ID.String(), pipeline.Name, map[string]any{
		"enabled":      enabled,
		"operation_id": operationIDOrEmpty(op),
	})
	RespondJSON(w, http.StatusOK, pipeline)
}

// FluentbitConfig handles GET /api/v1/logging/pipelines/{id}/fluentbit-config/.
// Returns a minimal Fluent Bit configuration stub for the pipeline's cluster.
func (h *LoggingHandler) FluentbitConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid pipeline ID")
		return
	}
	pipeline, err := h.queries.GetLoggingPipelineByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Logging pipeline not found")
		return
	}
	// TODO: render the full Fluent Bit configuration from all enabled
	// pipelines/outputs for this cluster once the renderer is ported. The
	// minimal stub below is enough for the UI to render the config view.
	config := "[SERVICE]\n    Daemon Off\n    Log_Level info\n\n[INPUT]\n    Name tail\n    Path /var/log/containers/*.log\n    Tag kube.*\n\n[OUTPUT]\n    Name stdout\n    Match *\n"
	RespondJSON(w, http.StatusOK, map[string]any{
		"cluster_id": pipeline.ClusterID.String(),
		"config":     config,
	})
}

// --- Operations endpoints ---

// ListOperations handles GET /api/v1/logging/operations/.
func (h *LoggingHandler) ListOperations(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 50))
	offset := int32(queryInt(r, "offset", 0))
	arg := sqlc.ListLoggingOperationsParams{Limit: limit, Offset: offset}
	if v := strings.TrimSpace(r.URL.Query().Get("targetType")); v != "" {
		arg.TargetType = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("targetKey")); v != "" {
		arg.TargetKey = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		arg.Status = pgtype.Text{String: v, Valid: true}
	}
	ops, err := h.queries.ListLoggingOperations(r.Context(), arg)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list logging operations")
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
			clusterID, err := h.loggingOperationClusterID(r.Context(), op)
			if err != nil || !h.authz.allowsCluster(bindings, clusterID, rbac.ResourceLogging, rbac.VerbRead) {
				continue
			}
		}
		items = append(items, loggingOperationResponse(op))
	}
	RespondJSON(w, http.StatusOK, items)
}

// GetOperation handles GET /api/v1/logging/operations/{id}/.
func (h *LoggingHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid operation ID")
		return
	}
	op, err := h.queries.GetLoggingOperation(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Logging operation not found")
		return
	}
	clusterID, err := h.loggingOperationClusterID(r.Context(), op)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "resolve_error", "Failed to resolve logging operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceLogging, rbac.VerbRead) {
		return
	}
	resp := loggingOperationResponse(op)
	if events, err := h.queries.ListLoggingOperationEvents(r.Context(), op.ID); err == nil {
		resp["events"] = loggingOperationEventsResponse(events)
	}
	RespondJSON(w, http.StatusOK, resp)
}

// RetryOperation handles POST /api/v1/logging/operations/{id}/retry/.
func (h *LoggingHandler) RetryOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid operation ID")
		return
	}
	op, err := h.queries.GetLoggingOperation(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Logging operation not found")
		return
	}
	if op.Status != "failed" && op.Status != "superseded" {
		RespondError(w, http.StatusConflict, "invalid_state", "Only failed or superseded operations can be retried")
		return
	}
	clusterID, err := h.loggingOperationClusterID(r.Context(), op)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "resolve_error", "Failed to resolve logging operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceLogging, rbac.VerbUpdate) {
		return
	}
	requeued, err := h.queries.RequeueLoggingOperation(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "retry_error", "Failed to retry logging operation")
		return
	}
	h.TriggerReconcile()
	recordAudit(r, h.queries, "logging.operation.retry", "logging_operation", id.String(), op.TargetKey, map[string]any{
		"target_type":     op.TargetType,
		"previous_status": op.Status,
	})
	RespondJSON(w, http.StatusAccepted, loggingOperationResponse(requeued))
}

// --- Reconciler internals ---

// loggingOperationEnvelope is the payload format we persist on every
// logging_operations row. The reconciler uses it to find the target cluster
// and to render the ConfigMap body without needing to re-fetch the source
// row (which may have been deleted in the delete case).
type loggingOperationEnvelope struct {
	ClusterID     string          `json:"cluster_id"`
	TargetID      string          `json:"target_id"`
	TargetType    string          `json:"target_type"`
	Name          string          `json:"name"`
	OutputType    string          `json:"output_type,omitempty"`
	Enabled       bool            `json:"enabled"`
	Configuration json.RawMessage `json:"configuration,omitempty"`
	Namespaces    json.RawMessage `json:"namespaces,omitempty"`
	Labels        json.RawMessage `json:"labels,omitempty"`
	Filters       json.RawMessage `json:"filters,omitempty"`
}

func (h *LoggingHandler) enqueueOutputApply(ctx context.Context, output sqlc.LoggingOutput, userID pgtype.UUID) (sqlc.LoggingOperation, error) {
	if !output.ClusterID.Valid {
		return sqlc.LoggingOperation{}, errors.New("logging output has no cluster_id")
	}
	env := loggingOperationEnvelope{
		ClusterID:     uuid.UUID(output.ClusterID.Bytes).String(),
		TargetID:      output.ID.String(),
		TargetType:    "output",
		Name:          output.Name,
		OutputType:    output.OutputType,
		Enabled:       output.Enabled,
		Configuration: output.Configuration,
	}
	return h.enqueueOperation(ctx, "output", output.ID.String(), "apply", env, userID)
}

func (h *LoggingHandler) enqueueOutputDelete(ctx context.Context, output sqlc.LoggingOutput, userID pgtype.UUID) (sqlc.LoggingOperation, error) {
	clusterID := ""
	if output.ClusterID.Valid {
		clusterID = uuid.UUID(output.ClusterID.Bytes).String()
	}
	env := loggingOperationEnvelope{
		ClusterID:  clusterID,
		TargetID:   output.ID.String(),
		TargetType: "output",
		Name:       output.Name,
		OutputType: output.OutputType,
	}
	return h.enqueueOperation(ctx, "output", output.ID.String(), "delete", env, userID)
}

func (h *LoggingHandler) enqueuePipelineApply(ctx context.Context, pipeline sqlc.LoggingPipeline, userID pgtype.UUID) (sqlc.LoggingOperation, error) {
	env := loggingOperationEnvelope{
		ClusterID:  pipeline.ClusterID.String(),
		TargetID:   pipeline.ID.String(),
		TargetType: "pipeline",
		Name:       pipeline.Name,
		Enabled:    pipeline.Enabled,
		Namespaces: pipeline.Namespaces,
		Labels:     pipeline.Labels,
		Filters:    pipeline.Filters,
	}
	return h.enqueueOperation(ctx, "pipeline", pipeline.ID.String(), "apply", env, userID)
}

func (h *LoggingHandler) enqueuePipelineDelete(ctx context.Context, pipeline sqlc.LoggingPipeline, userID pgtype.UUID) (sqlc.LoggingOperation, error) {
	env := loggingOperationEnvelope{
		ClusterID:  pipeline.ClusterID.String(),
		TargetID:   pipeline.ID.String(),
		TargetType: "pipeline",
		Name:       pipeline.Name,
	}
	return h.enqueueOperation(ctx, "pipeline", pipeline.ID.String(), "delete", env, userID)
}

func (h *LoggingHandler) enqueueOperation(ctx context.Context, targetType, targetKey, operationType string, env loggingOperationEnvelope, userID pgtype.UUID) (sqlc.LoggingOperation, error) {
	payload, err := json.Marshal(env)
	if err != nil {
		return sqlc.LoggingOperation{}, err
	}
	op, err := h.queries.CreateLoggingOperation(ctx, sqlc.CreateLoggingOperationParams{
		TargetType:    targetType,
		TargetKey:     targetKey,
		OperationType: operationType,
		Payload:       payload,
		Status:        "pending",
		CreatedByID:   userID,
	})
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

// processPendingOperations walks the pending queue once, coalescing
// duplicate target operations and applying the latest one. Mirrors the
// catalog handler's loop.
func (h *LoggingHandler) processPendingOperations(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListPendingLoggingOperations(ctx, 20)
	if err != nil {
		if h.log != nil {
			h.log.Warn("logging reconciler: list pending failed", "error", err)
		}
		return
	}
	// For each (target_type, target_key) keep only the newest op; older
	// pending ones get marked 'superseded' so the reconciler doesn't
	// double-apply intermediate states.
	latestByTarget := map[string]uuid.UUID{}
	for i := len(ops) - 1; i >= 0; i-- {
		key := ops[i].TargetType + ":" + ops[i].TargetKey
		if _, ok := latestByTarget[key]; !ok {
			latestByTarget[key] = ops[i].ID
		}
	}
	for _, op := range ops {
		key := op.TargetType + ":" + op.TargetKey
		if latestID, ok := latestByTarget[key]; ok && latestID != op.ID {
			h.recordEvent(ctx, op.ID, "info", "queue", "operation superseded by newer desired state", map[string]any{
				"targetType": op.TargetType,
				"targetKey":  op.TargetKey,
			})
			_, _ = h.queries.MarkLoggingOperationSuperseded(ctx, sqlc.MarkLoggingOperationSupersededParams{
				ID:           op.ID,
				ErrorMessage: "superseded by newer operation for target",
			})
			continue
		}
		// Avoid stampeding a still-fresh running row (e.g. another
		// process picked it up moments ago).
		if op.Status == "running" && op.StartedAt.Valid && time.Since(op.StartedAt.Time) < time.Minute {
			continue
		}
		running, err := h.queries.MarkLoggingOperationRunning(ctx, op.ID)
		if err != nil {
			continue
		}
		h.recordEvent(ctx, running.ID, "info", "queue", "operation execution started", map[string]any{
			"operationType": running.OperationType,
			"targetType":    running.TargetType,
			"targetKey":     running.TargetKey,
			"attemptCount":  running.AttemptCount,
		})
		if err := h.executeOperation(ctx, running); err != nil {
			h.recordEvent(ctx, running.ID, "error", "complete", "operation failed", map[string]any{"error": err.Error()})
			_, _ = h.queries.MarkLoggingOperationFailed(ctx, sqlc.MarkLoggingOperationFailedParams{
				ID:           running.ID,
				ErrorMessage: err.Error(),
			})
			if h.log != nil {
				h.log.Warn("logging operation failed", "id", running.ID.String(), "error", err)
			}
			continue
		}
		h.recordEvent(ctx, running.ID, "info", "complete", "operation completed", map[string]any{})
		_, _ = h.queries.MarkLoggingOperationCompleted(ctx, running.ID)
	}
}

// executeOperation renders the ConfigMap and applies (or deletes) it via the
// tunnel K8sRequester. Fluent Bit on the managed cluster is assumed to be
// running and watching the LoggingNamespace; this code does NOT install it.
func (h *LoggingHandler) executeOperation(ctx context.Context, op sqlc.LoggingOperation) error {
	var env loggingOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	if env.ClusterID == "" {
		return errors.New("operation payload missing cluster_id")
	}
	if h.requester == nil {
		return errors.New("k8s requester not configured")
	}
	configMapName := loggingConfigMapName(env.TargetType, env.TargetID)
	switch op.OperationType {
	case "apply":
		data, err := h.renderConfigMapData(env)
		if err != nil {
			return fmt.Errorf("render configmap: %w", err)
		}
		h.recordEvent(ctx, op.ID, "info", "apply", "applying logging configmap", map[string]any{
			"clusterId":     env.ClusterID,
			"namespace":     LoggingNamespace,
			"configMapName": configMapName,
		})
		// Ensure the namespace exists; Fluent Bit installer creates it
		// in steady state but on a fresh cluster the apply would 404.
		if err := ensureNamespace(ctx, h.requester, env.ClusterID, LoggingNamespace); err != nil {
			return fmt.Errorf("ensure namespace: %w", err)
		}
		return applyConfigMap(ctx, h.requester, env.ClusterID, LoggingNamespace, configMapName, data)
	case "delete":
		h.recordEvent(ctx, op.ID, "info", "delete", "deleting logging configmap", map[string]any{
			"clusterId":     env.ClusterID,
			"namespace":     LoggingNamespace,
			"configMapName": configMapName,
		})
		return deleteConfigMap(ctx, h.requester, env.ClusterID, LoggingNamespace, configMapName)
	default:
		return fmt.Errorf("unsupported logging operation type: %s", op.OperationType)
	}
}

// renderConfigMapData turns the envelope into the ConfigMap data block.
//
// For now this is intentionally a minimal placeholder render: we ship the
// canonical metadata (name, type, enabled, raw configuration JSON) so the
// ConfigMap is structurally present on the cluster and Fluent Bit's config
// reloader (or a downstream operator) can pick it up. The actual Fluent Bit
// snippet rendering — the [OUTPUT] / [FILTER] blocks — is the obvious
// follow-up and is left as a TODO so the architectural win (DB-backed
// operations + reconciler + ConfigMap roundtrip) lands first.
func (h *LoggingHandler) renderConfigMapData(env loggingOperationEnvelope) (map[string]string, error) {
	meta := map[string]any{
		"target_id":   env.TargetID,
		"target_type": env.TargetType,
		"name":        env.Name,
		"enabled":     env.Enabled,
		"placeholder": true,
	}
	if env.OutputType != "" {
		meta["output_type"] = env.OutputType
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, err
	}
	data := map[string]string{
		"meta.json": string(metaBytes),
	}
	if len(env.Configuration) > 0 {
		data["configuration.json"] = string(env.Configuration)
	}
	if len(env.Namespaces) > 0 {
		data["namespaces.json"] = string(env.Namespaces)
	}
	if len(env.Labels) > 0 {
		data["labels.json"] = string(env.Labels)
	}
	if len(env.Filters) > 0 {
		data["filters.json"] = string(env.Filters)
	}
	return data, nil
}

func loggingConfigMapName(targetType, targetID string) string {
	// Kubernetes ConfigMap names must be DNS-1123 subdomain compliant. UUID
	// + "logging-output-" / "logging-pipeline-" prefix gives us 36 + 16 < 63
	// chars and stays lowercase.
	prefix := "logging-" + targetType + "-"
	return prefix + strings.ToLower(targetID)
}

func deleteConfigMap(ctx context.Context, requester K8sRequester, clusterID, namespace, name string) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/configmaps/%s", namespace, name)
	resp, err := requester.Do(ctx, clusterID, http.MethodDelete, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return ensureSuccess(resp)
}

func ensureNamespace(ctx context.Context, requester K8sRequester, clusterID, name string) error {
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]any{"name": name},
	})
	if err != nil {
		return err
	}
	resp, err := requester.Do(ctx, clusterID, http.MethodPost, "/api/v1/namespaces", body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		// Already exists — that's fine.
		return nil
	}
	return ensureSuccess(resp)
}

func (h *LoggingHandler) recordEvent(ctx context.Context, operationID uuid.UUID, level, stage, message string, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, _ = h.queries.CreateLoggingOperationEvent(ctx, sqlc.CreateLoggingOperationEventParams{
		OperationID: operationID,
		Level:       level,
		Stage:       stage,
		Message:     message,
		Detail:      raw,
	})
}

// --- Response helpers ---

func loggingOperationResponse(op sqlc.LoggingOperation) map[string]any {
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

func loggingOperationEventsResponse(events []sqlc.LoggingOperationEvent) []map[string]any {
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

// loggingOperationClusterID resolves the target cluster of a logging
// operation row by decoding its payload envelope (the canonical source —
// every enqueue path sets ClusterID). For older rows that may have an
// empty ClusterID (e.g. a delete enqueued before the row carried it), we
// fall back to looking up the underlying output/pipeline by target_key.
func (h *LoggingHandler) loggingOperationClusterID(ctx context.Context, op sqlc.LoggingOperation) (uuid.UUID, error) {
	var env loggingOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err == nil && env.ClusterID != "" {
		return uuid.Parse(env.ClusterID)
	}
	// Fallback: hydrate from the underlying row. The reconciler may have
	// persisted a payload without cluster_id on legacy operations; we keep
	// authz correct by resolving through the target table.
	targetID, parseErr := uuid.Parse(op.TargetKey)
	if parseErr != nil {
		return uuid.UUID{}, parseErr
	}
	switch op.TargetType {
	case "output":
		out, err := h.queries.GetLoggingOutputByID(ctx, targetID)
		if err != nil {
			return uuid.UUID{}, err
		}
		if !out.ClusterID.Valid {
			return uuid.UUID{}, errors.New("logging output has no cluster_id")
		}
		return uuid.UUID(out.ClusterID.Bytes), nil
	case "pipeline":
		pipe, err := h.queries.GetLoggingPipelineByID(ctx, targetID)
		if err != nil {
			return uuid.UUID{}, err
		}
		return pipe.ClusterID, nil
	default:
		return uuid.UUID{}, fmt.Errorf("unknown logging operation target type: %s", op.TargetType)
	}
}

func operationIDOrEmpty(op sqlc.LoggingOperation) string {
	if op.ID == uuid.Nil {
		return ""
	}
	return op.ID.String()
}

// --- shared helpers (unchanged) ---

func clusterIDFromRequest(r *http.Request) (uuid.UUID, error) {
	if id := chi.URLParam(r, "cluster_id"); id != "" {
		return uuid.Parse(id)
	}
	return uuid.Parse(r.URL.Query().Get("cluster_id"))
}

func clusterIDFromRequestOrBody(r *http.Request, raw json.RawMessage) (uuid.UUID, error) {
	if id, err := clusterIDFromRequest(r); err == nil {
		return id, nil
	}
	var body struct {
		ClusterID string `json:"cluster_id"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &body) == nil && body.ClusterID != "" {
		return uuid.Parse(body.ClusterID)
	}
	return uuid.Nil, errors.New("cluster_id is required")
}
