package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *ToolHandler) ListOperations(w http.ResponseWriter, r *http.Request) {
	limit := queryLimit(r, 50)
	offset := queryOffset(r)
	arg := sqlc.ListToolOperationsParams{Limit: int32(limit), Offset: int32(offset)}
	if v := strings.TrimSpace(r.URL.Query().Get("targetType")); v != "" {
		arg.TargetType = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("targetKey")); v != "" {
		arg.TargetKey = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		arg.Status = pgtype.Text{String: v, Valid: true}
	}
	all, clusterIDs, _, err := h.authz.authorizedScopeIDs(r.Context(), rbac.ResourceCatalog, rbac.VerbRead, rbac.NarrowedClustersWiden)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	var ops []sqlc.ToolOperation
	var total int64
	pager, hasPager := h.queries.(toolOperationPager)
	if all {
		ops, err = h.queries.ListToolOperations(r.Context(), arg)
		if err == nil && hasPager {
			total, err = pager.CountToolOperations(r.Context(), sqlc.CountToolOperationsParams{
				TargetType: arg.TargetType, TargetKey: arg.TargetKey, Status: arg.Status,
			})
		}
	} else {
		if !hasPager {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Scoped tool-operation pagination is unavailable")
			return
		}
		ops, err = pager.ListToolOperationsForScopes(r.Context(), sqlc.ListToolOperationsForScopesParams{
			TargetType: arg.TargetType, TargetKey: arg.TargetKey, Status: arg.Status,
			ClusterIds: clusterIDs, QueryLimit: int32(limit), QueryOffset: int32(offset),
		})
		if err == nil {
			total, err = pager.CountToolOperationsForScopes(r.Context(), sqlc.CountToolOperationsForScopesParams{
				TargetType: arg.TargetType, TargetKey: arg.TargetKey, Status: arg.Status, ClusterIds: clusterIDs,
			})
		}
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list tool operations")
		return
	}
	items := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		items = append(items, toolOperationResponse(op))
	}
	if !hasPager {
		paging.Write(w, items, paging.FromPage(limit, offset, len(ops)))
		return
	}
	paging.Write(w, items, paging.Exact(total, limit, offset, len(ops)))
}

func (h *ToolHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetToolOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Tool operation not found")
		return
	}
	clusterID, err := toolOperationClusterID(op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve tool operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}
	resp := toolOperationResponse(op)
	if events, err := h.queries.ListToolOperationEvents(r.Context(), op.ID); err == nil {
		resp["events"] = toolOperationEventsResponse(events)
	}
	RespondJSON(w, http.StatusOK, resp)
}

func (h *ToolHandler) RetryOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetToolOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Tool operation not found")
		return
	}
	if !requireRetryableOperation(w, r, op.Status) {
		return
	}
	clusterID, err := toolOperationClusterID(op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve tool operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbUpdate) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	requeued, err := executeMutation(r, h.runTx,
		func(q ToolMutationTx) (sqlc.ToolOperation, error) { return q.RequeueToolOperation(r.Context(), id) },
		func(requeued sqlc.ToolOperation) mutationAuditEvent {
			return mutationAuditEvent{action: "tool.operation.retry", resourceType: "tool_operation", resourceID: id.String(), resourceName: op.TargetKey, status: http.StatusAccepted, detail: map[string]any{
				"target_type": op.TargetType, "previous_status": op.Status,
			}}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.RetryError, "Failed to retry tool operation")
		return
	}
	h.afterToolOperationCommit(requeued)
	RespondAcceptedOperation(w, "/api/v1/tools/operations/"+requeued.ID.String()+"/", toolOperationResponse(requeued))
}

func toolOperationClusterID(op sqlc.ToolOperation) (uuid.UUID, error) {
	var env toolOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return uuid.UUID{}, err
	}
	return uuid.Parse(env.ClusterID)
}
