package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/agentlifecycle"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/google/uuid"
)

func (h *ClusterAgentHandler) UpgradePlan(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ClusterAgentUnavailable, "Cluster agent inventory is not configured")
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	var req agentUpgradePlanRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	_, plan, err := h.buildUpgradePlanForCluster(r.Context(), clusterID, req)
	if err != nil {
		respondClusterAgentError(w, r, err)
		return
	}
	RespondJSON(w, http.StatusOK, plan)
}

func (h *ClusterAgentHandler) Upgrade(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ClusterAgentUnavailable, "Cluster agent inventory is not configured")
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	var req agentUpgradePlanRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	cluster, plan, err := h.buildUpgradePlanForCluster(r.Context(), clusterID, req)
	if err != nil {
		respondClusterAgentError(w, r, err)
		return
	}
	if !plan.Ready {
		RespondJSON(w, http.StatusConflict, map[string]any{
			"plan":     plan,
			"blockers": plan.Blockers,
		})
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "agent lifecycle transaction runner is not configured")
		return
	}
	spec, err := json.Marshal(map[string]any{
		"request":      req,
		"plan":         plan,
		"queued_at":    h.now().UTC().Format(time.RFC3339),
		"cluster_name": firstNonEmptyAgentValue(cluster.DisplayName, cluster.Name),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode lifecycle operation")
		return
	}
	params := sqlc.CreateAgentLifecycleOperationParams{
		ClusterID:      cluster.ID,
		OperationType:  agentlifecycle.OperationTypeUpgrade,
		TargetVersion:  plan.TargetVersion,
		TargetImage:    plan.TargetImage,
		CurrentVersion: plan.CurrentVersion,
		Strategy:       plan.Strategy,
		OperationSpec:  spec,
		RequestedBy:    currentUserUUID(r),
	}
	r = r.WithContext(withOperationIdempotency(r, "agent_lifecycle"))
	digest, err := canonicalOperationRequestDigest(struct {
		ClusterID  string                  `json:"cluster_id"`
		Request    agentUpgradePlanRequest `json:"request"`
		PlanDigest string                  `json:"plan_digest"`
	}{ClusterID: cluster.ID.String(), Request: req, PlanDigest: plan.PlanDigest})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode lifecycle request")
		return
	}
	receipt := agentUpgradeOperationResponse{Operation: agentLifecycleOperationDTO(sqlc.AgentLifecycleOperation{}), Plan: plan}
	replayed := false
	var op sqlc.AgentLifecycleOperation
	err = h.runTx(r.Context(), func(q ClusterAgentMutationTx) error {
		idemQ, ok := q.(resourceOperationIdempotencyQuerier)
		if !ok {
			return errors.New("agent lifecycle idempotency store is not configured")
		}
		_, stored, replay, claimErr := claimOperationReceipt[agentUpgradeOperationResponse](r.Context(), idemQ, "agent_lifecycle_operations", digest)
		if claimErr != nil {
			return claimErr
		}
		if replay {
			receipt = stored
			replayed = true
			return nil
		}
		var createErr error
		op, createErr = createAgentLifecycleOperation(r.Context(), q, params)
		if createErr != nil {
			return createErr
		}
		if op.ClusterID != params.ClusterID || op.OperationType != params.OperationType || op.TargetVersion != params.TargetVersion || op.TargetImage != params.TargetImage || op.Strategy != params.Strategy {
			return errOperationIdempotencyConflict
		}
		receipt = agentUpgradeOperationResponse{Operation: agentLifecycleOperationDTO(op), Plan: plan}
		if auditErr := recordAuditOutbox(r, q, "agent.upgrade.queued", "agent_lifecycle_operation", op.ID.String(), firstNonEmptyAgentValue(cluster.DisplayName, cluster.Name), http.StatusAccepted, map[string]any{
			"cluster_id": cluster.ID.String(), "current_version": plan.CurrentVersion,
			"target_version": plan.TargetVersion, "strategy": plan.Strategy,
			"configuration_digest": plan.ConfigurationDigest, "plan_digest": plan.PlanDigest,
		}); auditErr != nil {
			return auditErr
		}
		return attachOperationReceipt(r.Context(), idemQ, "agent_lifecycle_operations", op.ID, digest, receipt)
	})
	if errors.Is(err, errOperationIdempotencyConflict) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different agent upgrade")
		return
	}
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to queue agent upgrade operation")
		return
	}
	if !replayed {
		h.publishClusterAgentChanged(cluster.ID, op.ID.String())
	}
	RespondAcceptedOperation(w, "/api/v1/cluster-agents/"+cluster.ID.String()+"/operations/", receipt)
}

func createAgentLifecycleOperation(ctx context.Context, q ClusterAgentQuerier, params sqlc.CreateAgentLifecycleOperationParams) (sqlc.AgentLifecycleOperation, error) {
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		type idempotentCreator interface {
			CreateAgentLifecycleOperationIdempotent(context.Context, sqlc.CreateAgentLifecycleOperationIdempotentParams) (sqlc.AgentLifecycleOperation, error)
		}
		if creator, ok := q.(idempotentCreator); ok {
			return creator.CreateAgentLifecycleOperationIdempotent(ctx, sqlc.CreateAgentLifecycleOperationIdempotentParams{
				Scope:          idem.scope,
				IdempotencyKey: idem.key,
				ClusterID:      params.ClusterID,
				OperationType:  params.OperationType,
				TargetVersion:  params.TargetVersion,
				TargetImage:    params.TargetImage,
				CurrentVersion: params.CurrentVersion,
				Strategy:       params.Strategy,
				OperationSpec:  params.OperationSpec,
				RequestedBy:    params.RequestedBy,
			})
		}
	}
	return q.CreateAgentLifecycleOperation(ctx, params)
}

func (h *ClusterAgentHandler) Operations(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ClusterAgentUnavailable, "Cluster agent inventory is not configured")
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	ops, err := h.queries.ListAgentLifecycleOperationsByCluster(r.Context(), sqlc.ListAgentLifecycleOperationsByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list agent lifecycle operations")
		return
	}
	items := make([]agentLifecycleOperationResponse, 0, len(ops))
	for _, op := range ops {
		items = append(items, agentLifecycleOperationDTO(op))
	}
	paging.Write(w, items, paging.FromPage(int(limit), int(offset), len(items)))
}

func (h *ClusterAgentHandler) buildUpgradePlanForCluster(ctx context.Context, clusterID uuid.UUID, req agentUpgradePlanRequest) (sqlc.Cluster, agentUpgradePlanResponse, error) {
	cluster, err := h.queries.GetClusterByID(ctx, clusterID)
	if err != nil {
		return sqlc.Cluster{}, agentUpgradePlanResponse{}, &clusterAgentHandlerError{status: http.StatusNotFound, code: "not_found", message: "Cluster not found"}
	}
	connections, err := h.queries.ListConnectionsByCluster(ctx, sqlc.ListConnectionsByClusterParams{
		ClusterID: clusterID,
		Limit:     1,
		Offset:    0,
	})
	if err != nil {
		return sqlc.Cluster{}, agentUpgradePlanResponse{}, &clusterAgentHandlerError{status: http.StatusInternalServerError, code: "connection_error", message: "Failed to load agent connection state"}
	}
	now := h.now().UTC()
	conn := sqlc.AgentConnection{}
	connected := false
	if len(connections) > 0 {
		conn = connections[0]
		connected = conn.Status == "connected"
	}
	agent := buildClusterAgentItem(cluster, conn, connected, now)
	plan, err := h.buildUpgradePlan(cluster, agent, req)
	if err != nil {
		return sqlc.Cluster{}, agentUpgradePlanResponse{}, &clusterAgentHandlerError{status: http.StatusInternalServerError, code: apierror.InternalError, message: "Failed to render agent configuration"}
	}
	return cluster, plan, nil
}
