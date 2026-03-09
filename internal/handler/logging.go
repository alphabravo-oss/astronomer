package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// LoggingQuerier abstracts the logging-related database queries needed by LoggingHandler.
type LoggingQuerier interface {
	// Outputs
	ListLoggingOutputs(ctx context.Context, arg sqlc.ListLoggingOutputsParams) ([]sqlc.LoggingOutput, error)
	ListOutputsByCluster(ctx context.Context, arg sqlc.ListOutputsByClusterParams) ([]sqlc.LoggingOutput, error)
	GetLoggingOutputByID(ctx context.Context, id uuid.UUID) (sqlc.LoggingOutput, error)
	CreateLoggingOutput(ctx context.Context, arg sqlc.CreateLoggingOutputParams) (sqlc.LoggingOutput, error)
	DeleteLoggingOutput(ctx context.Context, id uuid.UUID) error
	CountLoggingOutputs(ctx context.Context) (int64, error)
	// Pipelines
	ListLoggingPipelines(ctx context.Context, arg sqlc.ListLoggingPipelinesParams) ([]sqlc.LoggingPipeline, error)
	ListPipelinesByCluster(ctx context.Context, arg sqlc.ListPipelinesByClusterParams) ([]sqlc.LoggingPipeline, error)
	GetLoggingPipelineByID(ctx context.Context, id uuid.UUID) (sqlc.LoggingPipeline, error)
	CreateLoggingPipeline(ctx context.Context, arg sqlc.CreateLoggingPipelineParams) (sqlc.LoggingPipeline, error)
	DeleteLoggingPipeline(ctx context.Context, id uuid.UUID) error
	CountLoggingPipelines(ctx context.Context) (int64, error)
}

// LoggingHandler handles logging output and pipeline endpoints.
type LoggingHandler struct {
	queries LoggingQuerier
}

// NewLoggingHandler creates a new logging handler.
func NewLoggingHandler(queries LoggingQuerier) *LoggingHandler {
	return &LoggingHandler{queries: queries}
}

// --- Request types ---

// CreateLoggingOutputRequest represents the request body for creating a logging output.
type CreateLoggingOutputRequest struct {
	Name          string          `json:"name"`
	OutputType    string          `json:"output_type"`
	Configuration json.RawMessage `json:"configuration"`
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
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
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
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	var req CreateLoggingOutputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
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
		CreatedByID:   pgtype.UUID{}, // TODO: extract from auth context
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create logging output")
		return
	}

	RespondJSON(w, http.StatusCreated, output)
}

// DeleteOutput handles DELETE /api/v1/clusters/{cluster_id}/logging/outputs/{id}/.
func (h *LoggingHandler) DeleteOutput(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid output ID")
		return
	}

	if err := h.queries.DeleteLoggingOutput(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Logging output not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Pipeline endpoints ---

// ListPipelines handles GET /api/v1/clusters/{cluster_id}/logging/pipelines/.
func (h *LoggingHandler) ListPipelines(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
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
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	var req CreateLoggingPipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
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
		CreatedByID: pgtype.UUID{}, // TODO: extract from auth context
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create logging pipeline")
		return
	}

	RespondJSON(w, http.StatusCreated, pipeline)
}

// DeletePipeline handles DELETE /api/v1/clusters/{cluster_id}/logging/pipelines/{id}/.
func (h *LoggingHandler) DeletePipeline(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid pipeline ID")
		return
	}

	if err := h.queries.DeleteLoggingPipeline(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Logging pipeline not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
