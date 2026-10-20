package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

func (h *LoggingHandler) GetPipeline(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apiutil.WriteError(w, r, 400, apiutil.InvalidID, "Invalid pipeline ID")
		return
	}
	pipeline, err := h.queries.GetLoggingPipelineByID(r.Context(), id)
	if err != nil {
		apiutil.WriteError(w, r, 404, apiutil.NotFound, "Logging pipeline not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, pipeline.ClusterID, rbac.ResourceLogging, rbac.VerbRead) {
		return
	}
	rows, err := h.loggingPipelineDTOs(r.Context(), []sqlc.LoggingPipeline{pipeline})
	if err != nil {
		apiutil.WriteError(w, r, 500, apiutil.InternalError, "Failed to load logging pipeline outputs")
		return
	}
	RespondJSON(w, http.StatusOK, rows[0])
}
