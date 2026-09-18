package handler

import (
	"net/http"
	"sort"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	projectdomain "github.com/alphabravocompany/astronomer-go/internal/projects"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *ProjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
		return
	}

	project, err := h.queries.GetProjectByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}

	// Migration 057: refuse / defer project.delete during an active
	// maintenance window. The cluster-scope check uses the parent
	// cluster's labels so a tier=prod cluster's projects inherit that
	// scope without an extra label set on the project itself.
	labels := map[string]string{}
	if cluster, cerr := h.queries.GetClusterByID(r.Context(), project.ClusterID); cerr == nil {
		labels = MaintenanceGateClusterLabels(cluster)
	}
	if EnforceMaintenanceWindow(w, r, h.maintenanceGate, "project.delete",
		labels,
		pgtype.UUID{Bytes: project.ClusterID, Valid: true},
		pgtype.UUID{Bytes: id, Valid: true}) {
		return
	}

	// Enqueue cleanup for every namespace before removing the project so the
	// managed CRs don't outlive their owner.
	for _, ns := range projectdomain.Namespaces(project.Namespaces) {
		if err := h.service.EnqueueRemove(r.Context(), project.ID, project.ClusterID, ns); err != nil {
			h.logger().Warn("persist project cleanup intent", "project_id", project.ID, "namespace", ns, "error", err)
		}
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}
	// The FK cascade (migration 021) drops this project's project_namespaces
	// and project_role_bindings rows, so the DB converges — but the RBAC
	// binding cache does not, and every member would keep the project's
	// synthetic namespace-scoped cluster bindings (read/exec on the namespaces
	// this handler just enqueued for cleanup) until their entry expires.
	// Same flush AddNamespace/RemoveNamespace do.
	h.invalidateRBACCache()
	h.recordProjectAudit(r, "project.delete", project, map[string]any{"clusterId": project.ClusterID.String()})

	w.WriteHeader(http.StatusNoContent)
}

// ListByCluster handles GET /api/v1/clusters/{cluster_id}/projects/.
// ListClusters handles GET /api/v1/projects/{id}/clusters/.
//
// T4.3 — the multi-cluster project view. Returns the distinct
// clusters the project is materialised on, derived from the
// project_namespaces rows. Each entry includes the cluster's display
// name and a count of namespaces the project has on that cluster, so
// the frontend can render a "this project lives on 3 clusters"
// breakdown without N+1 lookups.
func (h *ProjectHandler) ListClusters(w http.ResponseWriter, r *http.Request) {
	projectIDStr := chi.URLParam(r, "id")
	projectID, err := uuid.Parse(projectIDStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
		return
	}
	rows, err := h.queries.ListProjectNamespaces(r.Context(), projectID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list project namespaces")
		return
	}
	// Aggregate distinct cluster_ids with namespace counts.
	type clusterEntry struct {
		ClusterID      uuid.UUID `json:"cluster_id"`
		ClusterName    string    `json:"cluster_name"`
		NamespaceCount int       `json:"namespace_count"`
	}
	counts := map[uuid.UUID]int{}
	for _, row := range rows {
		counts[row.ClusterID]++
	}
	out := make([]clusterEntry, 0, len(counts))
	for cid, n := range counts {
		name := ""
		if c, gerr := h.queries.GetClusterByID(r.Context(), cid); gerr == nil {
			name = firstNonEmptyStr(c.DisplayName, c.Name)
		}
		out = append(out, clusterEntry{ClusterID: cid, ClusterName: name, NamespaceCount: n})
	}
	// Stable: alpha by name, falling back to id ordering when names tie.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ClusterName != out[j].ClusterName {
			return out[i].ClusterName < out[j].ClusterName
		}
		return out[i].ClusterID.String() < out[j].ClusterID.String()
	})
	RespondJSON(w, http.StatusOK, map[string]any{
		"project_id": projectID.String(),
		"clusters":   out,
		"count":      len(out),
	})
}

// firstNonEmptyStr is a small helper for picking display fallback
// strings.
func firstNonEmptyStr(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func (h *ProjectHandler) ListByCluster(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}

	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))
	search := strings.TrimSpace(r.URL.Query().Get("search"))

	projects, err := h.queries.ListProjectsByCluster(r.Context(), sqlc.ListProjectsByClusterParams{
		ClusterID:    clusterID,
		FilterSearch: search,
		QueryLimit:   limit,
		QueryOffset:  offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list projects")
		return
	}

	var total int64
	if search == "" {
		total, err = h.queries.CountProjectsByCluster(r.Context(), clusterID)
	} else {
		total, err = h.queries.CountProjectsByClusterFiltered(r.Context(), sqlc.CountProjectsByClusterFilteredParams{
			ClusterID:    clusterID,
			FilterSearch: search,
		})
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count projects")
		return
	}

	items := make([]ProjectResponse, 0, len(projects))
	for _, p := range projects {
		items = append(items, projectToResponse(p))
	}

	paging.Write(w, items, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(items)))
}
