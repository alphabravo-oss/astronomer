package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
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
	// helmConcurrency caps the parallel dispatch fan-out for
	// executeOperation; zero falls back to the package default.
	helmConcurrency int
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to load logging outputs")
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

	ops, _ := h.queries.ListLoggingOperations(ctx, sqlc.ListLoggingOperationsParams{Limit: 500, Offset: 0})
	opSummary := summarizeOperations(ctx, ops, operationStatusSummaryConfig[sqlc.LoggingOperation]{
		Status:    func(op sqlc.LoggingOperation) string { return op.Status },
		CreatedAt: func(op sqlc.LoggingOperation) time.Time { return op.CreatedAt },
		IsStaleRunning: func(op sqlc.LoggingOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) > time.Minute
		},
		Preview: func(_ context.Context, op sqlc.LoggingOperation) map[string]any { return loggingOperationResponse(op) },
		IsFailure: func(op sqlc.LoggingOperation) bool {
			return op.Status == OpStatusFailed
		},
		StaleThresholdSeconds: 60,
	})
	return map[string]any{
		"reconciler":    opSummary.reconcilerMap(),
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
		"operations":         opSummary.Counts,
		"recentFailureCount": opSummary.RecentFailures,
		"recentOperations":   opSummary.Recent,
		"latestFailure":      opSummary.LatestFailure,
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list logging outputs")
		return
	}

	total, err := h.queries.CountLoggingOutputs(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count logging outputs")
		return
	}

	RespondPaginated(w, r, outputs, total)
}

// CreateOutput handles POST /api/v1/clusters/{cluster_id}/logging/outputs/.
func (h *LoggingHandler) CreateOutput(w http.ResponseWriter, r *http.Request) {
	var req CreateLoggingOutputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}

	if req.Name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Output name is required")
		return
	}

	if req.OutputType == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Output type is required")
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create logging output")
		return
	}

	op, opErr := h.enqueueOutputApply(withOperationIdempotency(r, "logging"), output, currentUserUUID(r))
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid output ID")
		return
	}
	var req CreateLoggingOutputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update logging output")
		return
	}
	op, opErr := h.enqueueOutputApply(withOperationIdempotency(r, "logging"), output, currentUserUUID(r))
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid output ID")
		return
	}
	output, err := h.queries.GetLoggingOutputByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging output not found")
		return
	}
	op, err := h.enqueueOutputApply(withOperationIdempotency(r, "logging"), output, currentUserUUID(r))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EnqueueError, "Failed to enqueue apply test")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid output ID")
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
		op, opErr := h.enqueueOutputDelete(withOperationIdempotency(r, "logging"), existing, currentUserUUID(r))
		if opErr != nil && h.log != nil {
			h.log.Warn("logging: failed to enqueue output delete", "id", id.String(), "error", opErr)
		}
		deleteOp = op
	}

	if err := h.queries.DeleteLoggingOutput(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging output not found")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list logging pipelines")
		return
	}

	total, err := h.queries.CountLoggingPipelines(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count logging pipelines")
		return
	}

	RespondPaginated(w, r, pipelines, total)
}

// CreatePipeline handles POST /api/v1/clusters/{cluster_id}/logging/pipelines/.
func (h *LoggingHandler) CreatePipeline(w http.ResponseWriter, r *http.Request) {
	var req CreateLoggingPipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	clusterID, err := clusterIDFromRequest(r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}

	if req.Name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Pipeline name is required")
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create logging pipeline")
		return
	}

	op, opErr := h.enqueuePipelineApply(withOperationIdempotency(r, "logging"), pipeline, currentUserUUID(r))
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid pipeline ID")
		return
	}
	var req CreateLoggingPipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update logging pipeline")
		return
	}
	op, opErr := h.enqueuePipelineApply(withOperationIdempotency(r, "logging"), pipeline, currentUserUUID(r))
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid pipeline ID")
		return
	}

	existing, lookupErr := h.queries.GetLoggingPipelineByID(r.Context(), id)
	pipelineName := ""
	if lookupErr == nil {
		pipelineName = existing.Name
	}
	var deleteOp sqlc.LoggingOperation
	if lookupErr == nil {
		op, opErr := h.enqueuePipelineDelete(withOperationIdempotency(r, "logging"), existing, currentUserUUID(r))
		if opErr != nil && h.log != nil {
			h.log.Warn("logging: failed to enqueue pipeline delete", "id", id.String(), "error", opErr)
		}
		deleteOp = op
	}

	if err := h.queries.DeleteLoggingPipeline(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging pipeline not found")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid output ID")
		return
	}
	current, err := h.queries.GetLoggingOutputByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging output not found")
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update logging output")
		return
	}
	// Re-render the ConfigMap on enable/disable so cluster state tracks
	// intent. When disabled we still apply — the rendered config will
	// reflect enabled=false so Fluent Bit can skip it.
	op, opErr := h.enqueueOutputApply(withOperationIdempotency(r, "logging"), output, currentUserUUID(r))
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid output ID")
		return
	}
	if _, err := h.queries.GetLoggingOutputByID(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging output not found")
		return
	}
	// TODO: Implement per-backend log query (Loki/ES/etc) once a query client
	// is wired through. For now respond with a clear not-implemented status.
	RespondRequestError(w, r, http.StatusNotImplemented, apierror.NotImplemented, "Querying logs from this output backend is not yet implemented")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid pipeline ID")
		return
	}
	current, err := h.queries.GetLoggingPipelineByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging pipeline not found")
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update logging pipeline")
		return
	}
	op, opErr := h.enqueuePipelineApply(withOperationIdempotency(r, "logging"), pipeline, currentUserUUID(r))
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid pipeline ID")
		return
	}
	pipeline, err := h.queries.GetLoggingPipelineByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging pipeline not found")
		return
	}
	config := h.renderFullFluentbitConfig(r.Context(), pipeline.ClusterID)
	RespondJSON(w, http.StatusOK, map[string]any{
		"cluster_id": pipeline.ClusterID.String(),
		"config":     config,
	})
}

// FluentBitConfigMapName is the single aggregate config ConfigMap the controller
// writes and Fluent Bit consumes (mounted via the chart's existingConfigMap).
const FluentBitConfigMapName = "astronomer-fluent-bit-config"

// refreshAggregateFluentBitConfig re-renders the cluster's full Fluent Bit
// config from all enabled outputs/pipelines and writes it to the single
// aggregate ConfigMap that the Fluent Bit DaemonSet mounts. This is the link
// that makes configured outputs actually take effect: the installed Fluent Bit
// reads this ConfigMap (existingConfigMap) and hot-reloads on change.
func (h *LoggingHandler) refreshAggregateFluentBitConfig(ctx context.Context, clusterID string) error {
	cu, err := uuid.Parse(clusterID)
	if err != nil {
		return fmt.Errorf("parse cluster id: %w", err)
	}
	config := h.renderFullFluentbitConfig(ctx, cu)
	if err := ensureNamespace(ctx, h.requester, clusterID, LoggingNamespace); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}
	return applyConfigMap(ctx, h.requester, clusterID, LoggingNamespace, FluentBitConfigMapName, map[string]string{
		"fluent-bit.conf": config,
		// Standard parsers referenced by [SERVICE] Parsers_File; existingConfigMap
		// mounts only the keys in this ConfigMap, so we ship parsers here too.
		"parsers.conf": fluentBitDefaultParsers,
	})
}

// fluentBitDefaultParsers is a minimal parsers.conf covering CRI/Docker
// container-runtime log formats so the tail input can decode k8s logs.
const fluentBitDefaultParsers = `[PARSER]
    Name cri
    Format regex
    Regex ^(?<time>[^ ]+) (?<stream>stdout|stderr) (?<logtag>[^ ]*) (?<message>.*)$
    Time_Key time
    Time_Format %Y-%m-%dT%H:%M:%S.%L%z

[PARSER]
    Name docker
    Format json
    Time_Key time
    Time_Format %Y-%m-%dT%H:%M:%S.%L
    Time_Keep On
`

// renderFullFluentbitConfig assembles the complete Fluent Bit configuration for
// a cluster from its enabled pipelines (filters) and outputs, reusing the same
// block renderers the controller applies to the cluster. Previously this view
// returned a hardcoded stub that ignored the actual pipelines/outputs.
func (h *LoggingHandler) renderFullFluentbitConfig(ctx context.Context, clusterID uuid.UUID) string {
	var b strings.Builder
	b.WriteString("# rendered by astronomer-go logging controller\n")
	b.WriteString("[SERVICE]\n")
	writeKV(&b, "Daemon", "Off")
	writeKV(&b, "Log_Level", "info")
	writeKV(&b, "Parsers_File", "parsers.conf")
	// HTTP server + hot reload let the configmap-reload sidecar push new
	// outputs/pipelines without a pod restart when the controller rewrites
	// the aggregate config ConfigMap.
	writeKV(&b, "HTTP_Server", "On")
	writeKV(&b, "HTTP_Listen", "0.0.0.0")
	writeKV(&b, "HTTP_Port", "2020")
	writeKV(&b, "Hot_Reload", "On")
	b.WriteString("\n[INPUT]\n")
	writeKV(&b, "Name", "tail")
	writeKV(&b, "Path", "/var/log/containers/*.log")
	writeKV(&b, "Tag", "kube.*")
	writeKV(&b, "Mem_Buf_Limit", "5MB")
	writeKV(&b, "Skip_Long_Lines", "On")
	b.WriteString("\n[FILTER]\n")
	writeKV(&b, "Name", "kubernetes")
	writeKV(&b, "Match", "kube.*")
	writeKV(&b, "Merge_Log", "On")

	pipelines, err := h.queries.ListPipelinesByCluster(ctx, sqlc.ListPipelinesByClusterParams{ClusterID: clusterID, Limit: 500, Offset: 0})
	if err == nil {
		for _, p := range pipelines {
			if !p.Enabled {
				continue
			}
			b.WriteString("\n")
			b.WriteString(renderPipelineBlock(loggingOperationEnvelope{
				ClusterID: clusterID.String(), TargetID: p.ID.String(), TargetType: "pipeline",
				Name: p.Name, Enabled: p.Enabled, Namespaces: p.Namespaces, Labels: p.Labels, Filters: p.Filters,
			}))
		}
	}

	outputs, err := h.queries.ListOutputsByCluster(ctx, sqlc.ListOutputsByClusterParams{ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true}, Limit: 500, Offset: 0})
	enabledOutputs := 0
	if err == nil {
		for _, o := range outputs {
			if !o.Enabled {
				continue
			}
			enabledOutputs++
			b.WriteString("\n")
			b.WriteString(renderOutputBlock(loggingOperationEnvelope{
				ClusterID: clusterID.String(), TargetID: o.ID.String(), TargetType: "output",
				Name: o.Name, OutputType: o.OutputType, Enabled: o.Enabled, Configuration: o.Configuration,
			}))
		}
	}
	if enabledOutputs == 0 {
		// No output configured yet — stdout so logs are at least visible in the
		// Fluent Bit pod and the config is valid.
		b.WriteString("\n[OUTPUT]\n")
		writeKV(&b, "Name", "stdout")
		writeKV(&b, "Match", "*")
	}
	return b.String()
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list logging operations")
		return
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetLoggingOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging operation not found")
		return
	}
	clusterID, err := h.loggingOperationClusterID(r.Context(), op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve logging operation target")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetLoggingOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging operation not found")
		return
	}
	if !requireRetryableOperation(w, r, op.Status) {
		return
	}
	clusterID, err := h.loggingOperationClusterID(r.Context(), op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve logging operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceLogging, rbac.VerbUpdate) {
		return
	}
	requeued, err := h.queries.RequeueLoggingOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.RetryError, "Failed to retry logging operation")
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
	params := sqlc.CreateLoggingOperationParams{
		TargetType:    targetType,
		TargetKey:     targetKey,
		OperationType: operationType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	}
	var op sqlc.LoggingOperation
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		if creator, ok := h.queries.(interface {
			CreateLoggingOperationIdempotent(context.Context, sqlc.CreateLoggingOperationIdempotentParams) (sqlc.LoggingOperation, error)
		}); ok {
			op, err = creator.CreateLoggingOperationIdempotent(ctx, sqlc.CreateLoggingOperationIdempotentParams{
				Scope:          idem.scope,
				IdempotencyKey: idem.key,
				TargetType:     params.TargetType,
				TargetKey:      params.TargetKey,
				OperationType:  params.OperationType,
				Payload:        params.Payload,
				Status:         params.Status,
				CreatedByID:    params.CreatedByID,
			})
		}
	}
	if op.ID == uuid.Nil && err == nil {
		op, err = h.queries.CreateLoggingOperation(ctx, params)
	}
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

// processPendingOperations walks the pending queue once, coalescing
// duplicate target operations and applying the latest one. Mirrors the
// catalog handler's loop.
func (h *LoggingHandler) processPendingOperations(ctx context.Context) {
	// Claim under the lock, dispatch outside — same pattern as
	// catalog/tools/monitoring. One slow cluster must not block other
	// clusters' logging-config rollouts.
	dispatchClaimed(ctx, h.helmConcurrency, h.claimPendingLoggingOperations(ctx))
}

// claimPendingLoggingOperations holds h.mu just long enough to
// supersede stale targets and mark this tick's claims "running". Each
// returned claimedOp captures the row + the type-specific
// execute/complete/fail callbacks so dispatchClaimed can drive it
// without holding the lock.
func (h *LoggingHandler) claimPendingLoggingOperations(ctx context.Context) []claimedOp {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListPendingLoggingOperations(ctx, 20)
	if err != nil {
		if h.log != nil {
			h.log.Warn("logging reconciler: list pending failed", "error", err)
		}
		return nil
	}
	return claimLatestOperations(ctx, ops, operationRunnerConfig[sqlc.LoggingOperation]{
		ID:        func(op sqlc.LoggingOperation) uuid.UUID { return op.ID },
		TargetKey: func(op sqlc.LoggingOperation) string { return op.TargetType + ":" + op.TargetKey },
		Status:    func(op sqlc.LoggingOperation) string { return op.Status },
		IsFreshRunning: func(op sqlc.LoggingOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) < time.Minute
		},
		Supersede: func(ctx context.Context, op sqlc.LoggingOperation) {
			h.recordEvent(ctx, op.ID, "info", "queue", "operation superseded by newer desired state", map[string]any{
				"targetType": op.TargetType,
				"targetKey":  op.TargetKey,
			})
			_, _ = h.queries.MarkLoggingOperationSuperseded(ctx, sqlc.MarkLoggingOperationSupersededParams{
				ID:           op.ID,
				ErrorMessage: operationSupersededMessage,
			})
		},
		MarkRunning: func(ctx context.Context, op sqlc.LoggingOperation) (sqlc.LoggingOperation, error) {
			running, err := h.queries.MarkLoggingOperationRunning(ctx, op.ID)
			if err != nil {
				return sqlc.LoggingOperation{}, err
			}
			h.recordEvent(ctx, running.ID, "info", "queue", "operation execution started", map[string]any{
				"operationType": running.OperationType,
				"targetType":    running.TargetType,
				"targetKey":     running.TargetKey,
				"attemptCount":  running.AttemptCount,
			})
			return running, nil
		},
		Claimed: func(running sqlc.LoggingOperation) claimedOp {
			return claimedOp{
				ID: running.ID,
				Run: func(ctx context.Context) error {
					return h.executeOperation(ctx, running)
				},
				OnComplete: func(ctx context.Context) {
					h.recordEvent(ctx, running.ID, "info", "complete", "operation completed", map[string]any{})
					_, _ = h.queries.MarkLoggingOperationCompleted(ctx, running.ID)
				},
				OnFailure: func(ctx context.Context, err error) {
					h.recordEvent(ctx, running.ID, "error", "complete", "operation failed", map[string]any{"error": err.Error()})
					_, _ = h.queries.MarkLoggingOperationFailed(ctx, sqlc.MarkLoggingOperationFailedParams{
						ID:           running.ID,
						ErrorMessage: err.Error(),
					})
					if h.log != nil {
						h.log.Warn("logging operation failed", "id", running.ID.String(), "error", err)
					}
				},
			}
		},
	})
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
		if err := applyConfigMap(ctx, h.requester, env.ClusterID, LoggingNamespace, configMapName, data); err != nil {
			return err
		}
		// Refresh the single aggregate config ConfigMap that Fluent Bit actually
		// consumes (via existingConfigMap). The per-target ConfigMap above is
		// kept for provenance/debugging.
		return h.refreshAggregateFluentBitConfig(ctx, env.ClusterID)
	case "delete":
		h.recordEvent(ctx, op.ID, "info", "delete", "deleting logging configmap", map[string]any{
			"clusterId":     env.ClusterID,
			"namespace":     LoggingNamespace,
			"configMapName": configMapName,
		})
		if err := deleteConfigMap(ctx, h.requester, env.ClusterID, LoggingNamespace, configMapName); err != nil {
			return err
		}
		return h.refreshAggregateFluentBitConfig(ctx, env.ClusterID)
	default:
		return fmt.Errorf("unsupported logging operation type: %s", op.OperationType)
	}
}

// fluentBitValuePattern is the safe-character allowlist for values rendered
// into Fluent Bit config keys. Cluster names, label values, namespace names
// — anything that lands in a `Key Value` line — must match this. Anything
// that doesn't gets a `# warning` line instead of the real value, which
// keeps Fluent Bit's parser happy and prevents config injection from a
// malformed cluster name.
var fluentBitValuePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// renderConfigMapData turns the envelope into the ConfigMap data block.
//
// For outputs we render `output.conf` containing the Fluent Bit `[OUTPUT]`
// snippet plus a `meta.json` sidecar. For pipelines we render `pipeline.conf`
// with `[FILTER]` blocks and Match rules, plus `meta.json`. Each block lives
// in its own ConfigMap entry so a sidecar can compose them without parsing a
// concatenated megafile.
//
// Supported output_type values: elasticsearch, loki, s3, stdout. Anything
// else renders a `# unsupported output_type` comment so the operator sees
// the row was received but no live snippet was emitted.
func (h *LoggingHandler) renderConfigMapData(env loggingOperationEnvelope) (map[string]string, error) {
	generatedAt := time.Now().UTC().Format(time.RFC3339)
	meta := map[string]any{
		"id":           env.TargetID,
		"target_type":  env.TargetType,
		"name":         env.Name,
		"cluster_id":   env.ClusterID,
		"enabled":      env.Enabled,
		"generated_at": generatedAt,
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

	switch env.TargetType {
	case "output":
		data["output.conf"] = renderOutputBlock(env)
	case "pipeline":
		data["pipeline.conf"] = renderPipelineBlock(env)
	default:
		// Unknown target_type — fall back to a comment so the ConfigMap is
		// still structurally present without misleading config text.
		data["unknown.conf"] = "# unsupported target_type " + env.TargetType + "\n"
	}
	return data, nil
}

// renderOutputBlock renders the Fluent Bit `[OUTPUT]` snippet for one
// logging_outputs row. Unknown output_type values produce a comment line
// instead of a block so the reconciler keeps the ConfigMap up to date but
// Fluent Bit doesn't reject the file.
func renderOutputBlock(env loggingOperationEnvelope) string {
	cfg := decodeConfiguration(env.Configuration)
	var b strings.Builder
	b.WriteString("# rendered by astronomer-go logging controller\n")
	b.WriteString("# output: " + safeComment(env.Name) + " (" + env.OutputType + ")\n")
	if !env.Enabled {
		b.WriteString("# note: output is currently disabled\n")
	}
	switch env.OutputType {
	case "elasticsearch":
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "es")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		writeKV(&b, "Host", configString(cfg, "host", ""))
		writeKV(&b, "Port", configString(cfg, "port", "9200"))
		writeKV(&b, "Index", configString(cfg, "index", "astronomer"))
		if v := configString(cfg, "http_user", ""); v != "" {
			writeKV(&b, "HTTP_User", v)
		}
		if v := configString(cfg, "http_passwd", ""); v != "" {
			writeKV(&b, "HTTP_Passwd", v)
		}
		if v := configString(cfg, "tls", ""); v != "" {
			writeKV(&b, "tls", v)
		}
	case "loki":
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "loki")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		writeKV(&b, "Host", configString(cfg, "host", ""))
		writeKV(&b, "Port", configString(cfg, "port", "3100"))
		if v := configString(cfg, "labels", ""); v != "" {
			writeKV(&b, "Labels", v)
		}
		if v := configString(cfg, "tenant_id", ""); v != "" {
			writeKV(&b, "tenant_id", v)
		}
	case "s3":
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "s3")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		writeKV(&b, "bucket", configString(cfg, "bucket", ""))
		writeKV(&b, "region", configString(cfg, "region", "us-east-1"))
		if v := configString(cfg, "total_file_size", ""); v != "" {
			writeKV(&b, "total_file_size", v)
		}
		if v := configString(cfg, "upload_timeout", ""); v != "" {
			writeKV(&b, "upload_timeout", v)
		}
		if v := configString(cfg, "s3_key_format", ""); v != "" {
			writeKV(&b, "s3_key_format", v)
		}
	case "stdout":
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "stdout")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		if v := configString(cfg, "format", ""); v != "" {
			writeKV(&b, "Format", v)
		}
	case "splunk":
		host, port := outputHostPort(cfg, "hec_url", "8088")
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "splunk")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		writeKV(&b, "Host", host)
		writeKV(&b, "Port", port)
		if v := configString(cfg, "token", configString(cfg, "splunk_token", "")); v != "" {
			writeKV(&b, "Splunk_Token", v)
		}
		writeKV(&b, "Splunk_Send_Raw", "Off")
		writeKV(&b, "TLS", "On")
	case "datadog":
		site := configString(cfg, "site", "datadoghq.com")
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "datadog")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		writeKV(&b, "Host", "http-intake.logs."+site)
		writeKV(&b, "TLS", "on")
		writeKV(&b, "compress", "gzip")
		if v := configString(cfg, "api_key", ""); v != "" {
			writeKV(&b, "apikey", v)
		}
		if v := configString(cfg, "service", ""); v != "" {
			writeKV(&b, "dd_service", v)
		}
		if v := configString(cfg, "source", ""); v != "" {
			writeKV(&b, "dd_source", v)
		}
		if v := configString(cfg, "tags", ""); v != "" {
			writeKV(&b, "dd_tags", v)
		}
	case "cloudwatch":
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "cloudwatch_logs")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		writeKV(&b, "region", configString(cfg, "region", "us-east-1"))
		writeKV(&b, "log_group_name", configString(cfg, "log_group", "/astronomer/cluster-logs"))
		writeKV(&b, "log_stream_prefix", configString(cfg, "log_stream_prefix", "fluentbit-"))
		writeKV(&b, "auto_create_group", "On")
		if configString(cfg, "access_key", "") != "" {
			// The cloudwatch_logs plugin reads AWS credentials from the Fluent
			// Bit pod's identity (IRSA / instance role), not inline params.
			b.WriteString("# note: access_key/secret_key are supplied via the pod's AWS identity (IRSA/instance role)\n")
		}
	case "syslog":
		b.WriteString("[OUTPUT]\n")
		writeKV(&b, "Name", "syslog")
		writeKV(&b, "Match", configString(cfg, "match", "*"))
		writeKV(&b, "Host", configString(cfg, "host", ""))
		writeKV(&b, "Port", configString(cfg, "port", "514"))
		writeKV(&b, "Mode", configString(cfg, "protocol", "tcp"))
		writeKV(&b, "Syslog_Format", configString(cfg, "format", "rfc5424"))
		writeKV(&b, "Syslog_Maxsize", "2048")
	default:
		b.WriteString("# unsupported output_type " + env.OutputType + "; no [OUTPUT] emitted\n")
	}
	return b.String()
}

// outputHostPort extracts host + port for an output, accepting either a full
// URL field (e.g. "https://host:8088") or separate host/port config keys.
func outputHostPort(cfg map[string]any, urlKey, defaultPort string) (host, port string) {
	port = defaultPort
	if raw := configString(cfg, urlKey, ""); raw != "" {
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
			host = u.Hostname()
			if u.Port() != "" {
				port = u.Port()
			}
			return host, port
		}
	}
	host = configString(cfg, "host", "")
	if p := configString(cfg, "port", ""); p != "" {
		port = p
	}
	return host, port
}

// renderPipelineBlock renders Match rules for the pipeline's namespaces and
// `[FILTER]` blocks for any modify labels / declared filters. Pipelines
// don't emit [OUTPUT] blocks themselves — those come from the linked
// logging_outputs rows, rendered into separate ConfigMaps.
func renderPipelineBlock(env loggingOperationEnvelope) string {
	var b strings.Builder
	b.WriteString("# rendered by astronomer-go logging controller\n")
	b.WriteString("# pipeline: " + safeComment(env.Name) + "\n")
	if !env.Enabled {
		b.WriteString("# note: pipeline is currently disabled\n")
	}

	namespaces := decodeStringList(env.Namespaces)
	if len(namespaces) == 0 {
		b.WriteString("# no namespaces declared; matches all kube.* records\n")
	}
	for _, ns := range namespaces {
		if !fluentBitValuePattern.MatchString(ns) {
			b.WriteString("# warning: skipped invalid namespace " + safeComment(ns) + "\n")
			continue
		}
		b.WriteString("# match kube." + ns + ".*\n")
	}

	labels := decodeStringMap(env.Labels)
	if len(labels) > 0 {
		matchPattern := pipelineMatchPattern(namespaces)
		b.WriteString("[FILTER]\n")
		writeKV(&b, "Name", "modify")
		writeKV(&b, "Match", matchPattern)
		// Sort keys so renders are deterministic — important for unit tests
		// and for avoiding spurious diffs in ConfigMap apply traffic.
		keys := make([]string, 0, len(labels))
		for k := range labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := labels[k]
			if !fluentBitValuePattern.MatchString(k) {
				b.WriteString("# warning: skipped invalid label " + safeComment(k) + "\n")
				continue
			}
			if !fluentBitValuePattern.MatchString(v) {
				b.WriteString("# warning: skipped invalid label " + safeComment(k) + "\n")
				continue
			}
			b.WriteString("    Add         " + k + " " + v + "\n")
		}
	}

	for _, f := range decodeFilters(env.Filters) {
		if f.Type == "" {
			b.WriteString("# warning: skipped filter with empty type\n")
			continue
		}
		b.WriteString("[FILTER]\n")
		writeKV(&b, "Name", f.Type)
		writeKV(&b, "Match", pipelineMatchPattern(namespaces))
		// Stable iteration order across params for the same reasons as above.
		keys := make([]string, 0, len(f.Params))
		for k := range f.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := f.Params[k]
			if !fluentBitValuePattern.MatchString(k) {
				b.WriteString("# warning: skipped invalid param " + safeComment(k) + "\n")
				continue
			}
			if !fluentBitValuePattern.MatchString(v) {
				b.WriteString("# warning: skipped invalid param " + safeComment(k) + "\n")
				continue
			}
			writeKV(&b, k, v)
		}
	}
	return b.String()
}

// pipelineMatchPattern collapses the namespace list into a single Match tag
// pattern. Fluent Bit supports a glob, so for one namespace we emit
// kube.<ns>.* and for many we use kube.* with a note above (the per-ns
// comment lines above let an operator audit what was intended).
func pipelineMatchPattern(namespaces []string) string {
	valid := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		if fluentBitValuePattern.MatchString(ns) {
			valid = append(valid, ns)
		}
	}
	if len(valid) == 1 {
		return "kube." + valid[0] + ".*"
	}
	return "kube.*"
}

// loggingFilterSpec mirrors the per-filter shape we accept inside a
// pipeline's filters JSON column: {type: "...", params: {k: v, ...}}.
type loggingFilterSpec struct {
	Type   string            `json:"type"`
	Params map[string]string `json:"params"`
}

func decodeFilters(raw json.RawMessage) []loggingFilterSpec {
	if len(raw) == 0 {
		return nil
	}
	var arr []loggingFilterSpec
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	// Tolerate the legacy `{}` default that older rows persist; treat it as
	// "no filters" rather than a parse error.
	return nil
}

func decodeStringList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil
	}
	return arr
}

func decodeStringMap(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	// Decode into RawMessage first so we can stringify non-string values
	// rather than dropping them silently — operators sometimes pass numeric
	// label values from the UI.
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil
	}
	out := make(map[string]string, len(generic))
	for k, v := range generic {
		switch t := v.(type) {
		case string:
			out[k] = t
		case bool:
			if t {
				out[k] = "true"
			} else {
				out[k] = "false"
			}
		case float64:
			out[k] = fmt.Sprintf("%v", t)
		}
	}
	return out
}

func decodeConfiguration(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	return m
}

func configString(cfg map[string]any, key, fallback string) string {
	if v, ok := cfg[key]; ok {
		switch t := v.(type) {
		case string:
			if t != "" {
				return t
			}
		case float64:
			return fmt.Sprintf("%v", t)
		case bool:
			if t {
				return "true"
			}
			return "false"
		}
	}
	return fallback
}

// writeKV renders one `    Key Value` line in Fluent Bit's classic config
// format. The leading four-space indent matches Fluent Bit's documented
// style; the renderer itself uses tabs (per CLAUDE.md house style) but the
// emitted config is what Fluent Bit will parse, so we keep its conventions.
func writeKV(b *strings.Builder, key, value string) {
	b.WriteString("    ")
	b.WriteString(key)
	b.WriteString(" ")
	b.WriteString(value)
	b.WriteString("\n")
}

// safeComment strips newlines from a string so it can't break out of a
// `# comment` line into the surrounding config.
func safeComment(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
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
