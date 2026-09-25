package handler

import (
	"context"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

func (h *ClusterSnapshotsHandler) SetAuthorization(engine *rbac.Engine, q rbac.BindingQuerier) {
	h.authz.SetAuthorization(engine, q)
}

type clusterRestorePager interface {
	ListClusterRestoresPage(context.Context, sqlc.ListClusterRestoresPageParams) ([]sqlc.ListClusterRestoresPageRow, error)
	CountClusterRestores(context.Context, sqlc.CountClusterRestoresParams) (int64, error)
}

// Restore records belong to the target cluster. Source snapshot contents remain
// behind the source cluster's own read gate, applied before pagination.
func (h *ClusterSnapshotsHandler) ListRestores(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceClusters, rbac.VerbRead) {
		return
	}
	pager, ok := h.queries.(clusterRestorePager)
	if !ok {
		RespondRequestError(w, r, 503, apierror.InternalError, "Restore history store unavailable")
		return
	}
	all, sources, _, err := h.authz.authorizedScopeIDs(r.Context(), rbac.ResourceClusters, rbac.VerbRead, rbac.NarrowedClustersExcluded)
	if err != nil {
		RespondRequestError(w, r, 500, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}
	limit, offset := queryLimitOffset(r, 50)
	rows, err := pager.ListClusterRestoresPage(r.Context(), sqlc.ListClusterRestoresPageParams{TargetClusterID: clusterID, AllSources: all, SourceClusterIds: sources, QueryLimit: int32(limit), QueryOffset: int32(offset)})
	if err != nil {
		RespondRequestError(w, r, 500, apierror.ListError, "Failed to list restores")
		return
	}
	total, err := pager.CountClusterRestores(r.Context(), sqlc.CountClusterRestoresParams{TargetClusterID: clusterID, AllSources: all, SourceClusterIds: sources})
	if err != nil {
		RespondRequestError(w, r, 500, apierror.CountError, "Failed to count restores")
		return
	}
	out := make([]RestoreResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, restorePageResponse(row))
	}
	paging.Write(w, out, paging.Exact(total, limit, offset, len(out)))
}

func (h *ClusterSnapshotsHandler) GetRestore(w http.ResponseWriter, r *http.Request) {
	clusterID, id, ok := parseClusterAndSnapshotIDs(w, r)
	if !ok {
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceClusters, rbac.VerbRead) {
		return
	}
	row, err := h.queries.GetClusterRestoreByID(r.Context(), id)
	if err != nil || row.TargetClusterID != clusterID {
		RespondRequestError(w, r, 404, apierror.NotFound, "Restore not found")
		return
	}
	snapshot, err := h.queries.GetClusterSnapshotByID(r.Context(), row.SnapshotID)
	if err != nil {
		RespondRequestError(w, r, 404, apierror.NotFound, "Restore not found")
		return
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, 500, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}
	if restricted && !h.authz.allowsCluster(bindings, snapshot.ClusterID, rbac.ResourceClusters, rbac.VerbRead) {
		RespondRequestError(w, r, 404, apierror.NotFound, "Restore not found")
		return
	}
	out := restoreToResponse(row)
	out.SourceClusterID = snapshot.ClusterID
	RespondJSON(w, http.StatusOK, out)
}

func restorePageResponse(row sqlc.ListClusterRestoresPageRow) RestoreResponse {
	out := restoreToResponse(sqlc.ClusterRestore{
		ID: row.ID, SnapshotID: row.SnapshotID, TargetClusterID: row.TargetClusterID, VeleroName: row.VeleroName, VeleroNamespace: row.VeleroNamespace,
		Spec: row.Spec, Phase: row.Phase, StartTime: row.StartTime, CompletionTime: row.CompletionTime, WarningsCount: row.WarningsCount, ErrorsCount: row.ErrorsCount,
		LastPollAt: row.LastPollAt, LastPollError: row.LastPollError, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	out.SourceClusterID = row.SourceClusterID
	return out
}
