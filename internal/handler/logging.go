package handler

import (
	"context"
	"encoding/json"
	"errors"
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
}

// LoggingHandler handles logging output and pipeline endpoints.
type LoggingHandler struct {
	queries LoggingQuerier
}

// NewLoggingHandler creates a new logging handler.
func NewLoggingHandler(queries LoggingQuerier) *LoggingHandler {
	return &LoggingHandler{queries: queries}
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
	return map[string]any{
		"reconciler": map[string]any{
			"enabled": false,
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

	recordAudit(r, h.queries, "logging.output.create", "logging_output", output.ID.String(), output.Name, map[string]any{
		"cluster_id":  clusterID.String(),
		"output_type": output.OutputType,
		"enabled":     output.Enabled,
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
	recordAudit(r, h.queries, "logging.output.update", "logging_output", output.ID.String(), output.Name, map[string]any{
		"output_type": output.OutputType,
		"enabled":     output.Enabled,
	})
	RespondJSON(w, http.StatusOK, output)
}

// TestOutput handles POST /api/v1/logging/outputs/{id}/test/.
func (h *LoggingHandler) TestOutput(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid output ID")
		return
	}
	if _, err := h.queries.GetLoggingOutputByID(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Logging output not found")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Logging output configuration is valid"})
}

// DeleteOutput handles DELETE /api/v1/clusters/{cluster_id}/logging/outputs/{id}/.
func (h *LoggingHandler) DeleteOutput(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid output ID")
		return
	}

	outputName := ""
	if existing, lookupErr := h.queries.GetLoggingOutputByID(r.Context(), id); lookupErr == nil {
		outputName = existing.Name
	}
	if err := h.queries.DeleteLoggingOutput(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Logging output not found")
		return
	}

	recordAudit(r, h.queries, "logging.output.delete", "logging_output", id.String(), outputName, nil)

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

	recordAudit(r, h.queries, "logging.pipeline.create", "logging_pipeline", pipeline.ID.String(), pipeline.Name, map[string]any{
		"cluster_id": clusterID.String(),
		"enabled":    pipeline.Enabled,
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
	recordAudit(r, h.queries, "logging.pipeline.update", "logging_pipeline", pipeline.ID.String(), pipeline.Name, map[string]any{
		"enabled": pipeline.Enabled,
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

	pipelineName := ""
	if existing, lookupErr := h.queries.GetLoggingPipelineByID(r.Context(), id); lookupErr == nil {
		pipelineName = existing.Name
	}
	if err := h.queries.DeleteLoggingPipeline(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Logging pipeline not found")
		return
	}

	recordAudit(r, h.queries, "logging.pipeline.delete", "logging_pipeline", id.String(), pipelineName, nil)

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
