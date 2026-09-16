package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/operationstate"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *WorkloadHandler) List(w http.ResponseWriter, r *http.Request) {
	clusterUUID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	clusterID := clusterUUID.String()
	namespace := r.URL.Query().Get("namespace")
	kind := r.URL.Query().Get("kind")
	search := strings.ToLower(r.URL.Query().Get("search"))

	workloads, err := h.listWorkloads(r.Context(), clusterID, namespace, kind)
	if err != nil {
		respondClusterAccessError(w, r, err)
		return
	}

	all, names, err := h.authz.authorizedNamespaces(r.Context(), clusterUUID, rbac.ResourceWorkloads, rbac.VerbList)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}
	if !all {
		workloads = filterItemsByNamespaceKey(workloads, "namespace", names)
	}

	filtered := make([]map[string]any, 0, len(workloads))
	for _, item := range workloads {
		if search != "" && !strings.Contains(strings.ToLower(item["name"].(string)), search) && !strings.Contains(strings.ToLower(item["namespace"].(string)), search) {
			continue
		}
		filtered = append(filtered, item)
	}
	// Slice server-side with the same shared limit/offset policy used to build
	// the response links instead of returning the whole cluster on every page.
	page, metadata := pageWindow(r, filtered)
	paging.Write(w, page, metadata)
}

func (h *WorkloadHandler) Get(w http.ResponseWriter, r *http.Request) {
	clusterUUID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	clusterID := clusterUUID.String()
	kind := chi.URLParam(r, "kind")
	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	resource, err := h.getWorkload(r.Context(), clusterID, kind, namespace, name)
	if err != nil {
		respondClusterAccessError(w, r, err)
		return
	}
	RespondJSON(w, http.StatusOK, resource)
}

func (h *WorkloadHandler) Scale(w http.ResponseWriter, r *http.Request) {
	clusterUUID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	clusterID := clusterUUID.String()
	kind, namespace, name := chi.URLParam(r, "kind"), chi.URLParam(r, "namespace"), chi.URLParam(r, "name")
	// openapi:request-operation patchClustersByClusterIdWorkloadsByKindByNamespaceByNameScale
	var req struct {
		Replicas int32 `json:"replicas"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if _, err := scalePath(kind, namespace, name); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidKind, err.Error())
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	op, err := h.createAuditedWorkloadOperation(r, "workload", workloadTargetKey(clusterID, kind, namespace, name), "scale", workloadOperationEnvelope{
		ClusterID: clusterID,
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
		Replicas:  req.Replicas,
	}, currentUserUUID(r), mutationAuditEvent{action: "workload.scale", resourceType: "workload", resourceID: kind + "/" + namespace + "/" + name, resourceName: name, status: http.StatusAccepted, detail: map[string]any{
		"clusterId": clusterID, "replicas": req.Replicas,
	}})
	if err != nil {
		respondWorkloadMutationError(w, r, err, apierror.EnqueueError, "Failed to enqueue workload scale")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/workloads/operations/"+op.ID.String()+"/", workloadOperationResponse(op))
}

func (h *WorkloadHandler) Restart(w http.ResponseWriter, r *http.Request) {
	clusterUUID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	clusterID := clusterUUID.String()
	kind, namespace, name := chi.URLParam(r, "kind"), chi.URLParam(r, "namespace"), chi.URLParam(r, "name")
	if _, err := workloadPath(kind, namespace, name); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidKind, err.Error())
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	op, err := h.createAuditedWorkloadOperation(r, "workload", workloadTargetKey(clusterID, kind, namespace, name), "restart", workloadOperationEnvelope{
		ClusterID: clusterID,
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
	}, currentUserUUID(r), mutationAuditEvent{action: "workload.restart", resourceType: "workload", resourceID: kind + "/" + namespace + "/" + name, resourceName: name, status: http.StatusAccepted, detail: map[string]any{"clusterId": clusterID}})
	if err != nil {
		respondWorkloadMutationError(w, r, err, apierror.EnqueueError, "Failed to enqueue workload restart")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/workloads/operations/"+op.ID.String()+"/", workloadOperationResponse(op))
}

func (h *WorkloadHandler) Delete(w http.ResponseWriter, r *http.Request) {
	clusterUUID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	clusterID := clusterUUID.String()
	kind, namespace, name := chi.URLParam(r, "kind"), chi.URLParam(r, "namespace"), chi.URLParam(r, "name")
	if _, err := workloadPath(kind, namespace, name); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidKind, err.Error())
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	op, err := h.createAuditedWorkloadOperation(r, "workload", workloadTargetKey(clusterID, kind, namespace, name), "delete", workloadOperationEnvelope{
		ClusterID: clusterID,
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
	}, currentUserUUID(r), mutationAuditEvent{action: "workload.delete", resourceType: "workload", resourceID: kind + "/" + namespace + "/" + name, resourceName: name, status: http.StatusAccepted, detail: map[string]any{"clusterId": clusterID}})
	if err != nil {
		respondWorkloadMutationError(w, r, err, apierror.EnqueueError, "Failed to enqueue workload delete")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/workloads/operations/"+op.ID.String()+"/", workloadOperationResponse(op))
}

func (h *WorkloadHandler) ListOperations(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.WorkloadError, "workload store not configured")
		return
	}
	// Clamp limit to a sane bound (and floor at 1) via the shared helper so a
	// hostile ?limit=2000000000 can't materialize the whole operations table,
	// and ?limit=2147483648 can't overflow int32 into a negative LIMIT that
	// Postgres rejects. Mirrors audit/smtp/webhooks/maintenance handlers.
	limit, offset := queryLimitOffset(r, 50)
	arg := sqlc.ListWorkloadOperationsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	}
	if v := strings.TrimSpace(r.URL.Query().Get("targetType")); v != "" {
		arg.TargetType = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("targetKey")); v != "" {
		arg.TargetKey = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		arg.Status = pgtype.Text{String: v, Valid: true}
	}
	ops, err := h.queries.ListWorkloadOperations(r.Context(), arg)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.WorkloadError, "Failed to list workload operations")
		return
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	resp := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		if restricted {
			clusterID, err := workloadOperationClusterID(op)
			if err != nil || !h.authz.allowsCluster(bindings, clusterID, rbac.ResourceWorkloads, rbac.VerbRead) {
				continue
			}
		}
		resp = append(resp, workloadOperationResponse(op))
	}
	RespondJSON(w, http.StatusOK, resp)
}

func (h *WorkloadHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetWorkloadOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Workload operation not found")
		return
	}
	caller, authenticated := reqctx.AuthenticatedUser(r.Context())
	if !authenticated || caller == nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	if !h.authorizeWorkloadOperationReceipt(w, r, caller.ID, op) {
		return
	}
	resp := workloadOperationResponse(op)
	if events, err := h.queries.ListWorkloadOperationEvents(r.Context(), op.ID); err == nil {
		resp["events"] = workloadOperationEventsResponse(events)
	}
	RespondJSON(w, http.StatusOK, resp)
}

const maxWorkloadOperationAuthorizationEnvelopeBytes = 64 << 10

func (h *WorkloadHandler) authorizeWorkloadOperationReceipt(w http.ResponseWriter, r *http.Request, callerIDText string, op sqlc.WorkloadOperation) bool {
	if len(op.Payload) == 0 || len(op.Payload) > maxWorkloadOperationAuthorizationEnvelopeBytes {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve workload operation target")
		return false
	}
	var target workloadOperationEnvelope
	if err := json.Unmarshal(op.Payload, &target); err != nil || target.ClusterID == "" || len(target.Namespace) > 253 {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve workload operation target")
		return false
	}
	clusterID, err := uuid.Parse(target.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve workload operation target")
		return false
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return false
	}
	if !restricted {
		return true
	}
	namespace := strings.TrimSpace(target.Namespace)
	if h.authz.engine.CheckPermission(bindings, rbac.ResourceWorkloads, rbac.VerbRead, clusterID, uuid.Nil, namespace) {
		return true
	}
	callerID, err := uuid.Parse(callerIDText)
	if err != nil || !op.CreatedByID.Valid || callerID != uuid.UUID(op.CreatedByID.Bytes) || !workloadOperationMutationScopeAllowed(r) {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to perform this action")
		return false
	}
	allowed := false
	switch op.OperationType {
	case "delete_pod":
		allowed = h.authz.engine.CheckPermission(bindings, rbac.ResourcePods, rbac.VerbDelete, clusterID, uuid.Nil, namespace)
	case "scale":
		allowed = h.authz.engine.CheckPermission(bindings, rbac.ResourceWorkloads, rbac.VerbScale, clusterID, uuid.Nil, namespace)
	case "restart":
		allowed = h.authz.engine.CheckPermission(bindings, rbac.ResourceWorkloads, rbac.VerbRestart, clusterID, uuid.Nil, namespace)
	case "delete":
		allowed = h.authz.engine.CheckPermission(bindings, rbac.ResourceWorkloads, rbac.VerbDelete, clusterID, uuid.Nil, namespace)
	case "vulnerability_rescan":
		allowed = h.authz.engine.CheckPermission(bindings, rbac.ResourceClusters, rbac.VerbUpdate, clusterID, uuid.Nil) &&
			h.authz.engine.CheckPermission(bindings, rbac.ResourceClusters, rbac.VerbRead, clusterID, uuid.Nil)
	}
	if !allowed {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to perform this action")
	}
	return allowed
}

func workloadOperationMutationScopeAllowed(r *http.Request) bool {
	caller, _ := reqctx.AuthenticatedUser(r.Context())
	if caller == nil || caller.AuthMethod != "api_token" {
		return true
	}
	token, ok := auth.AuthenticatedAPIToken(r.Context())
	if !ok || token == nil {
		return true
	}
	scopes, err := auth.ParseTokenScopes(token.Scopes)
	return err == nil && auth.ScopeAllowsRequest(scopes, auth.ScopeWriteClusters)
}

func (h *WorkloadHandler) RetryOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetWorkloadOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Workload operation not found")
		return
	}
	if !requireRetryableOperation(w, r, op.Status) {
		return
	}
	clusterID, err := workloadOperationClusterID(op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve workload operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceWorkloads, rbac.VerbUpdate) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	requeued, err := executeMutation(r, h.runTx,
		func(q WorkloadMutationTx) (sqlc.WorkloadOperation, error) {
			return q.RequeueWorkloadOperation(r.Context(), id)
		},
		func(requeued sqlc.WorkloadOperation) mutationAuditEvent {
			return mutationAuditEvent{action: "workload.operation.retry", resourceType: "workload_operation", resourceID: id.String(), resourceName: op.TargetKey, status: http.StatusAccepted, detail: map[string]any{
				"target_type": op.TargetType, "previous_status": op.Status,
			}}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.RetryError, "Failed to retry workload operation")
		return
	}
	h.TriggerReconcile()
	RespondAcceptedOperation(w, "/api/v1/workloads/operations/"+requeued.ID.String()+"/", workloadOperationResponse(requeued))
}

func (h *WorkloadHandler) ControllerStatus(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondJSON(w, http.StatusOK, map[string]any{"reconciler": map[string]any{"enabled": false, "queueDepth": 0}})
		return
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	// Fast path: unrestricted callers get exact, uncapped counts straight from
	// the database via a GROUP BY aggregation. This avoids streaming up to 1000
	// operation rows and recomputing per-operation authorization on every status
	// poll — the aggregation runs entirely server-side.
	if !restricted {
		rows, err := h.queries.CountWorkloadOperationsByStatus(r.Context())
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to load workload controller status")
			return
		}
		counts := make(map[string]int, len(rows))
		staleRunning := 0
		for _, row := range rows {
			counts[row.Status] = int(row.Total)
			staleRunning += int(row.StaleRunning)
		}
		summary := operationStatusSummary{
			Counts:                counts,
			StaleRunning:          staleRunning,
			StaleThresholdSeconds: 60,
		}
		summary.QueueDepth = operationstate.QueueDepth(counts)
		RespondJSON(w, http.StatusOK, map[string]any{
			"reconciler": summary.reconcilerMap(),
			"operations": summary.Counts,
		})
		return
	}
	// Restricted callers need per-operation cluster authorization, which depends
	// on the operation payload; fall back to loading rows and filtering in Go.
	ops, err := h.queries.ListWorkloadOperations(r.Context(), sqlc.ListWorkloadOperationsParams{Limit: 1000, Offset: 0})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to load workload controller status")
		return
	}
	opSummary := summarizeOperations(r.Context(), ops, operationStatusSummaryConfig[sqlc.WorkloadOperation]{
		Status:    func(op sqlc.WorkloadOperation) string { return op.Status },
		CreatedAt: func(op sqlc.WorkloadOperation) time.Time { return op.CreatedAt },
		IsStaleRunning: func(op sqlc.WorkloadOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) > time.Minute
		},
		Include: func(_ context.Context, op sqlc.WorkloadOperation) bool {
			clusterID, err := workloadOperationClusterID(op)
			return err == nil && h.authz.allowsCluster(bindings, clusterID, rbac.ResourceWorkloads, rbac.VerbRead)
		},
		Preview: func(_ context.Context, op sqlc.WorkloadOperation) map[string]any {
			return workloadOperationResponse(op)
		},
		StaleThresholdSeconds: 60,
	})
	RespondJSON(w, http.StatusOK, map[string]any{
		"reconciler": opSummary.reconcilerMap(),
		"operations": opSummary.Counts,
	})
}

func workloadOperationClusterID(op sqlc.WorkloadOperation) (uuid.UUID, error) {
	var env workloadOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return uuid.UUID{}, err
	}
	return uuid.Parse(env.ClusterID)
}
