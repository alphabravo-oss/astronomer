package handler

import (
	"context"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/google/uuid"
)

// List handles GET /clusters/{cluster_id}/shell/sessions/.
func (h *KubectlShellHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ShellUnavailable, "Kubectl shell is not configured")
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	limit, offset := queryLimitOffset(r, 100)
	rows, err := h.Queries.ListActiveKubectlSessionsByCluster(r.Context(), sqlc.ListActiveKubectlSessionsByClusterParams{
		ClusterID: clusterID, QueryLimit: int32(limit), QueryOffset: int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	counts, err := h.commandCountsForSessions(r.Context(), rows)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	out := make([]kubectl.SessionInfo, 0, len(rows))
	for _, row := range rows {
		out = append(out, kubectl.ToSessionInfo(row, counts[row.ID], h.idleTimeout()))
	}
	total, err := h.Queries.CountActiveKubectlSessionsByCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	paging.Write(w, out, paging.Exact(total, queryLimit(r, 100), queryOffset(r), len(out)))
}

// commandCountsForSessions loads the list decoration in one grouped query.
// Sessions with no recorded commands are intentionally absent from the SQL
// result and retain the map's zero value.
func (h *KubectlShellHandler) commandCountsForSessions(ctx context.Context, sessions []sqlc.KubectlSession) (map[uuid.UUID]int64, error) {
	counts := make(map[uuid.UUID]int64, len(sessions))
	if len(sessions) == 0 {
		return counts, nil
	}
	ids := make([]uuid.UUID, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, session.ID)
	}
	rows, err := h.Queries.CountKubectlSessionCommandsForSessions(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.SessionID] = row.CommandCount
	}
	return counts, nil
}

// AdminListAll handles GET /admin/shell-sessions/.
func (h *KubectlShellHandler) AdminListAll(w http.ResponseWriter, r *http.Request) {
	if !h.gateSuperuser(w, r) {
		return
	}
	limit := queryLimitMax(r, 100, 500)
	if limit < 1 {
		limit = 100
	}
	offset := queryOffset(r)
	rows, err := h.Queries.ListAllActiveKubectlSessionsPage(r.Context(), sqlc.ListAllActiveKubectlSessionsPageParams{
		QueryLimit:  int32(limit),
		QueryOffset: int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	counts, err := h.commandCountsForSessions(r.Context(), rows)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	total, err := h.Queries.CountAllActiveKubectlSessions(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	out := make([]kubectl.SessionInfo, 0, len(rows))
	for _, row := range rows {
		out = append(out, kubectl.ToSessionInfo(row, counts[row.ID], h.idleTimeout()))
	}
	paging.Write(w, out, paging.Exact(total, queryLimitMax(r, 100, 500), queryOffset(r), len(out)))
}
