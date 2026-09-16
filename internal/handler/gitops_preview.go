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
	rows, err := h.queries.ListGitOpsRegisteredClustersBySource(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list clusters")
		return
	}
	// Memoize the cluster lookup per request so a source with the same
	// cluster registered under multiple repo paths resolves the name once
	// instead of a GetClusterByID per row (N+1). Mirrors the instance→cluster
	// cache in delivery status views and the clusterName cache in projects.
	out := make([]map[string]any, 0, len(rows))
	clusterCache := make(map[uuid.UUID]sqlc.Cluster, len(rows))
	clusterMissing := make(map[uuid.UUID]struct{})
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
		cluster, ok := clusterCache[link.ClusterID]
		if !ok {
			if _, missed := clusterMissing[link.ClusterID]; !missed {
				c, cerr := h.queries.GetClusterByID(r.Context(), link.ClusterID)
				if cerr == nil {
					cluster, ok = c, true
					clusterCache[link.ClusterID] = c
				} else {
					clusterMissing[link.ClusterID] = struct{}{}
				}
			}
		}
		if ok {
			entry["cluster_name"] = cluster.Name
			entry["display_name"] = cluster.DisplayName
		}
		out = append(out, entry)
	}
	page, pagination := pageWindow(r, out)
	paging.Write(w, page, pagination)
}

// Helpers -------------------------------------------------------------
