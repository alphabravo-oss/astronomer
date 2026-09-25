package handler

import (
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (h *LoggingHandler) GetPipeline(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, 400, apierror.InvalidID, "Invalid pipeline ID")
		return
	}
	pipeline, err := h.queries.GetLoggingPipelineByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, 404, apierror.NotFound, "Logging pipeline not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, pipeline.ClusterID, rbac.ResourceLogging, rbac.VerbRead) {
		return
	}
	rows, err := h.loggingPipelineDTOs(r.Context(), []sqlc.LoggingPipeline{pipeline})
	if err != nil {
		RespondRequestError(w, r, 500, apierror.InternalError, "Failed to load logging pipeline outputs")
		return
	}
	RespondJSON(w, http.StatusOK, rows[0])
}
