package handler

import (
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Preview handles GET /api/v1/admin/gitops-sources/{id}/preview/.
func (h *GitOpsHandler) Preview(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	if h.runner == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "GitOps sync runner not configured")
		return
	}
	res, err := h.runner.PreviewSource(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.PreviewError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, res)
}

// ListClusters handles GET /api/v1/admin/gitops-sources/{id}/clusters/.
func (h *GitOpsHandler) ListClusters(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	limit, offset := queryLimitOffset(r, 20)
	rows, err := h.queries.ListGitOpsRegisteredClustersBySourcePage(r.Context(), sqlc.ListGitOpsRegisteredClustersBySourcePageParams{
		SourceID: id, QueryLimit: int32(limit), QueryOffset: int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list clusters")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, link := range rows {
		entry := map[string]any{
			"cluster_id":      link.ClusterID.String(),
			"repo_path":       link.RepoPath,
			"last_yaml_sha":   link.LastYamlSha,
			"last_applied_at": link.LastAppliedAt.UTC().Format(time.RFC3339),
			"status":          link.Status,
		}
		if link.TombstonedAt.Valid {
			entry["tombstoned_at"] = link.TombstonedAt.Time.UTC().Format(time.RFC3339)
		}
		if link.ClusterName.Valid {
			entry["cluster_name"] = link.ClusterName.String
		}
		if link.DisplayName.Valid {
			entry["display_name"] = link.DisplayName.String
		}
		out = append(out, entry)
	}
	total, err := h.queries.CountGitOpsRegisteredClustersBySource(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to count clusters")
		return
	}
	paging.Write(w, out, paging.Exact(total, limit, offset, len(out)))
}

// Helpers -------------------------------------------------------------
