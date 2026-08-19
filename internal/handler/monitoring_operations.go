package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func (h *MonitoringHandler) TriggerReconcile() {
	if h == nil || h.triggerCh == nil {
		return
	}
	select {
	case h.triggerCh <- struct{}{}:
	default:
	}
}

func (h *MonitoringHandler) StartReconciler(ctx context.Context) {
	if h == nil || h.queries == nil || h.helm == nil {
		return
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	go h.runReconciler(ctx)
}

func (h *MonitoringHandler) ListOperations(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondList(w, []any{}, NewPagination(0, queryLimit(r, 50), queryInt(r, "offset", 0), 0))
		return
	}
	limit := int32(queryLimit(r, 50))
	offset := int32(queryInt(r, "offset", 0))
	arg := sqlc.ListMonitoringOperationsParams{
		Limit:  limit,
		Offset: offset,
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
	items, err := h.queries.ListMonitoringOperations(r.Context(), arg)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to list monitoring operations")
		return
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	resp := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if restricted {
			allowed, err := h.canReadMonitoringOperation(r.Context(), bindings, item)
			if err != nil || !allowed {
				continue
			}
		}
		resp = append(resp, monitoringOperationResponse(item))
	}
	// List is filtered in-Go by RBAC; no COUNT matches the visible set.
	// has_more is inferred from the DB page (len(items)) being full, not the
	// post-filter resp, so next_offset advances over rows skipped by RBAC.
	RespondList(w, resp, NewPaginationFromPage(int(limit), int(offset), len(items)))
}

func (h *MonitoringHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MonitoringError, "monitoring store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetMonitoringOperation(r.Context(), id)
	if err != nil {
		if err == pgx.ErrNoRows {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Monitoring operation not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to load monitoring operation")
		return
	}
	if !h.authorizeMonitoringOperationRead(w, r, op) {
		return
	}
	resp := monitoringOperationResponse(op)
	if events, err := h.queries.ListMonitoringOperationEvents(r.Context(), op.ID); err == nil {
		resp["events"] = monitoringOperationEventsResponse(events)
	}
	RespondJSON(w, http.StatusOK, resp)
}

func (h *MonitoringHandler) RetryOperation(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MonitoringError, "monitoring store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetMonitoringOperation(r.Context(), id)
	if err != nil {
		if err == pgx.ErrNoRows {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Monitoring operation not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to load monitoring operation")
		return
	}
	if !requireRetryableOperation(w, r, op.Status) {
		return
	}
	if !h.authorizeMonitoringOperationUpdate(w, r, op) {
		return
	}
	requeued, err := h.queries.RequeueMonitoringOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to requeue monitoring operation")
		return
	}
	h.TriggerReconcile()
	recordAudit(r, h.queries, "monitoring.operation.retry", "monitoring_operation", id.String(), op.TargetKey, map[string]any{
		"target_type":     op.TargetType,
		"previous_status": op.Status,
	})
	RespondJSON(w, http.StatusAccepted, monitoringOperationResponse(requeued))
}

func (h *MonitoringHandler) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	h.processPendingMonitoringOperations(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.processPendingMonitoringOperations(ctx)
		case <-h.triggerCh:
			h.processPendingMonitoringOperations(ctx)
		}
	}
}

func (h *MonitoringHandler) enqueueSharedThanosOperation(ctx context.Context, userID pgtype.UUID, opType string, req SharedThanosStackRequest, values map[string]any, secretSpec *objectStoreSecretSpec) (sqlc.MonitoringOperation, error) {
	rawReq, err := json.Marshal(req)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	payload, err := json.Marshal(monitoringOperationEnvelope{
		ClusterID:                req.ManagementClusterID,
		Request:                  rawReq,
		Values:                   values,
		SecretSpec:               secretSpec,
		ResolvedAutoRollback:     h.resolveAutoRollbackPolicy(backend, req.AutoRollbackOnFailure),
		ResolvedMaxRetryAttempts: h.resolveMaxRetryAttempts(backend),
	})
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	params := sqlc.CreateMonitoringOperationParams{
		TargetType:    "shared_thanos",
		TargetKey:     "shared",
		OperationType: opType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	}
	op, err := h.createMonitoringOperation(ctx, params)
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

func (h *MonitoringHandler) enqueueSharedGrafanaOperation(ctx context.Context, userID pgtype.UUID, opType string, req SharedGrafanaRequest, values map[string]any) (sqlc.MonitoringOperation, error) {
	rawReq, err := json.Marshal(req)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	payload, err := json.Marshal(monitoringOperationEnvelope{
		ClusterID:                req.ManagementClusterID,
		Request:                  rawReq,
		Values:                   values,
		ResolvedAutoRollback:     h.resolveAutoRollbackPolicy(backend, req.AutoRollbackOnFailure),
		ResolvedMaxRetryAttempts: h.resolveMaxRetryAttempts(backend),
	})
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	params := sqlc.CreateMonitoringOperationParams{
		TargetType:    "shared_grafana",
		TargetKey:     "shared",
		OperationType: opType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	}
	op, err := h.createMonitoringOperation(ctx, params)
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

func (h *MonitoringHandler) enqueueSharedAlertmanagerOperation(ctx context.Context, userID pgtype.UUID, opType string, req SharedAlertmanagerRequest, values map[string]any) (sqlc.MonitoringOperation, error) {
	rawReq, err := json.Marshal(req)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	payload, err := json.Marshal(monitoringOperationEnvelope{
		ClusterID:                req.ManagementClusterID,
		Request:                  rawReq,
		Values:                   values,
		ResolvedAutoRollback:     h.resolveAutoRollbackPolicy(backend, req.AutoRollbackOnFailure),
		ResolvedMaxRetryAttempts: h.resolveMaxRetryAttempts(backend),
	})
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	params := sqlc.CreateMonitoringOperationParams{
		TargetType:    "shared_alertmanager",
		TargetKey:     "shared",
		OperationType: opType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	}
	op, err := h.createMonitoringOperation(ctx, params)
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

func (h *MonitoringHandler) enqueueClusterStackOperation(ctx context.Context, userID pgtype.UUID, opType, clusterID string, req MonitoringStackRequest, values map[string]any) (sqlc.MonitoringOperation, error) {
	// Defensive no-op for an unwired store: production always injects queries,
	// but the route-security tests reach here with a nil querier once a caller
	// clears the RBAC gate. Return a clean error so the handler answers 500
	// rather than dereferencing nil below.
	if h.queries == nil {
		return sqlc.MonitoringOperation{}, fmt.Errorf("monitoring store not configured")
	}
	rawReq, err := json.Marshal(req)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	payload, err := json.Marshal(monitoringOperationEnvelope{
		ClusterID:                clusterID,
		Request:                  rawReq,
		Values:                   values,
		ResolvedAutoRollback:     h.resolveAutoRollbackPolicy(backend, req.AutoRollbackOnFailure),
		ResolvedMaxRetryAttempts: h.resolveMaxRetryAttempts(backend),
	})
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	params := sqlc.CreateMonitoringOperationParams{
		TargetType:    "cluster_stack",
		TargetKey:     clusterID,
		OperationType: opType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	}
	op, err := h.createMonitoringOperation(ctx, params)
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

func (h *MonitoringHandler) createMonitoringOperation(ctx context.Context, params sqlc.CreateMonitoringOperationParams) (sqlc.MonitoringOperation, error) {
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		if creator, ok := h.queries.(interface {
			CreateMonitoringOperationIdempotent(context.Context, sqlc.CreateMonitoringOperationIdempotentParams) (sqlc.MonitoringOperation, error)
		}); ok {
			return creator.CreateMonitoringOperationIdempotent(ctx, sqlc.CreateMonitoringOperationIdempotentParams{
				Scope:          idem.scope,
				IdempotencyKey: idem.key,
				TargetType:     params.TargetType,
				TargetKey:      params.TargetKey,
				OperationType:  params.OperationType,
				Payload:        params.Payload,
				Status:         params.Status,
				CreatedByID:    params.CreatedByID,
			})
		}
	}
	return h.queries.CreateMonitoringOperation(ctx, params)
}

func monitoringOperationResponse(op sqlc.MonitoringOperation) map[string]any {
	return map[string]any{
		"id":            op.ID.String(),
		"targetType":    op.TargetType,
		"targetKey":     op.TargetKey,
		"operationType": op.OperationType,
		"status":        op.Status,
		"attemptCount":  op.AttemptCount,
		"startedAt":     nullablePgTime(op.StartedAt),
		"completedAt":   nullablePgTime(op.CompletedAt),
		"errorMessage":  op.ErrorMessage,
		"createdAt":     op.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":     op.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func monitoringOperationEventsResponse(events []sqlc.MonitoringOperationEvent) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		out = append(out, map[string]any{
			"id":        event.ID.String(),
			"level":     event.Level,
			"stage":     event.Stage,
			"message":   event.Message,
			"detail":    decodeJSONMap(event.Detail),
			"createdAt": event.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func (h *MonitoringHandler) latestMonitoringOperation(ctx context.Context, targetType, targetKey string) (map[string]any, bool) {
	if h.queries == nil {
		return nil, false
	}
	op, err := h.queries.GetLatestMonitoringOperationForTarget(ctx, sqlc.GetLatestMonitoringOperationForTargetParams{
		TargetType: targetType,
		TargetKey:  targetKey,
	})
	if err != nil {
		return nil, false
	}
	return monitoringOperationResponse(op), true
}

func (h *MonitoringHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	if h == nil || h.queries == nil {
		return map[string]any{
			"reconciler": map[string]any{"enabled": false, "queueDepth": 0},
			"operations": map[string]int{},
		}, nil
	}
	ops, err := h.queries.ListMonitoringOperations(ctx, sqlc.ListMonitoringOperationsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	bindings, restricted, err := h.authz.bindingsForContext(ctx)
	if err != nil {
		return nil, err
	}
	opSummary := summarizeOperations(ctx, ops, operationStatusSummaryConfig[sqlc.MonitoringOperation]{
		Status:    func(op sqlc.MonitoringOperation) string { return op.Status },
		CreatedAt: func(op sqlc.MonitoringOperation) time.Time { return op.CreatedAt },
		IsStaleRunning: func(op sqlc.MonitoringOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) > 2*time.Minute
		},
		Include: func(ctx context.Context, op sqlc.MonitoringOperation) bool {
			if !restricted {
				return true
			}
			allowed, err := h.canReadMonitoringOperation(ctx, bindings, op)
			return err == nil && allowed
		},
		Preview: func(ctx context.Context, op sqlc.MonitoringOperation) map[string]any {
			return h.monitoringOperationPreview(ctx, op)
		},
		StaleThresholdSeconds: 120,
	})
	summary := map[string]any{
		"reconciler":         opSummary.reconcilerMap(),
		"operations":         opSummary.Counts,
		"recentFailureCount": opSummary.RecentFailures,
		"recentOperations":   opSummary.Recent,
		"latestFailure":      opSummary.LatestFailure,
	}
	if backend, err := h.queries.GetDefaultMonitoringBackend(ctx); err == nil {
		metadata := decodeJSONMap(backend.AuthConfig)
		summary["backend"] = map[string]any{
			"type":     backend.BackendType,
			"queryUrl": backend.QueryUrl,
			"healthy":  strings.EqualFold(fmt.Sprint(metadata["status"]), "healthy"),
			"status":   firstNonEmptyString(fmt.Sprint(metadata["status"]), "unknown"),
		}
	}
	return summary, nil
}

func (h *MonitoringHandler) monitoringOperationPreview(ctx context.Context, op sqlc.MonitoringOperation) map[string]any {
	resp := monitoringOperationResponse(op)
	if events, err := h.queries.ListMonitoringOperationEvents(ctx, op.ID); err == nil && len(events) > 0 {
		resp["eventsPreview"] = monitoringOperationEventsResponse(lastMonitoringEvents(events, 3))
	}
	return resp
}

func (h *MonitoringHandler) authorizeMonitoringOperationRead(w http.ResponseWriter, r *http.Request, op sqlc.MonitoringOperation) bool {
	return h.authorizeMonitoringOperation(w, r, op, rbac.VerbRead)
}

func (h *MonitoringHandler) authorizeMonitoringOperationUpdate(w http.ResponseWriter, r *http.Request, op sqlc.MonitoringOperation) bool {
	return h.authorizeMonitoringOperation(w, r, op, rbac.VerbUpdate)
}

func (h *MonitoringHandler) authorizeMonitoringOperation(w http.ResponseWriter, r *http.Request, op sqlc.MonitoringOperation, verb rbac.Verb) bool {
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return false
	}
	if !restricted {
		return true
	}
	allowed, err := h.canAccessMonitoringOperation(r.Context(), bindings, op, verb)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve monitoring operation target")
		return false
	}
	if !allowed {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to access this operation")
		return false
	}
	return true
}

func (h *MonitoringHandler) canReadMonitoringOperation(ctx context.Context, bindings []rbac.RoleBinding, op sqlc.MonitoringOperation) (bool, error) {
	return h.canAccessMonitoringOperation(ctx, bindings, op, rbac.VerbRead)
}

func (h *MonitoringHandler) canAccessMonitoringOperation(ctx context.Context, bindings []rbac.RoleBinding, op sqlc.MonitoringOperation, verb rbac.Verb) (bool, error) {
	switch op.TargetType {
	case "shared_thanos", "shared_alertmanager", "shared_grafana":
		return h.authz.allowsGlobal(bindings, rbac.ResourceMonitoring, verb), nil
	case "cluster_stack":
		clusterID, err := uuid.Parse(op.TargetKey)
		if err != nil {
			return false, err
		}
		return h.authz.allowsCluster(bindings, clusterID, rbac.ResourceMonitoring, verb), nil
	default:
		return h.authz.allowsGlobal(bindings, rbac.ResourceMonitoring, verb), nil
	}
}

func lastMonitoringEvents(events []sqlc.MonitoringOperationEvent, n int) []sqlc.MonitoringOperationEvent {
	if len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}

func (h *MonitoringHandler) processPendingMonitoringOperations(ctx context.Context) {
	// Claim under the lock, dispatch outside it.
	// Same-target double-dispatch is still prevented by the supersession
	// pass below + DB row "running" state.
	dispatchClaimed(ctx, h.helmConcurrency, h.claimPendingMonitoringOperations(ctx))
}

// claimPendingMonitoringOperations supersedes stale targets and marks
// this tick's claims "running" while holding h.mu. Returned rows are
// wrapped as claimedOps; dispatchClaimed runs them outside the lock.
// Monitoring is special in that the OnFailure closure also re-emits the
// retry/requeue policy when AttemptCount < maxAttempts — that's why we
// capture maxAttempts at claim time (so we don't have to re-parse the
// payload from the dispatch goroutine).
func (h *MonitoringHandler) claimPendingMonitoringOperations(ctx context.Context) []claimedOp {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListPendingMonitoringOperations(ctx, 20)
	if err != nil {
		if h.log != nil {
			h.log.Warn("failed to list pending monitoring operations", "error", err)
		}
		return nil
	}
	return claimLatestOperations(ctx, ops, operationRunnerConfig[sqlc.MonitoringOperation]{
		ID:        func(op sqlc.MonitoringOperation) uuid.UUID { return op.ID },
		TargetKey: func(op sqlc.MonitoringOperation) string { return op.TargetType + ":" + op.TargetKey },
		Status:    func(op sqlc.MonitoringOperation) string { return op.Status },
		ShouldSupersede: func(op sqlc.MonitoringOperation, now time.Time) bool {
			return op.Status == OpStatusPending || op.Status == OpStatusRunning && (!op.StartedAt.Valid || now.Sub(op.StartedAt.Time) >= 2*time.Minute)
		},
		IsFreshRunning: func(op sqlc.MonitoringOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) < 2*time.Minute
		},
		Supersede: func(ctx context.Context, op sqlc.MonitoringOperation) {
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "queue", "operation superseded by newer desired state", map[string]any{
				"targetType": op.TargetType,
				"targetKey":  op.TargetKey,
			})
			_, _ = h.queries.MarkMonitoringOperationSuperseded(ctx, sqlc.MarkMonitoringOperationSupersededParams{
				ID:           op.ID,
				ErrorMessage: operationSupersededMessage,
			})
		},
		MarkRunning: func(ctx context.Context, op sqlc.MonitoringOperation) (sqlc.MonitoringOperation, error) {
			running, err := h.queries.MarkMonitoringOperationRunning(ctx, op.ID)
			if err != nil {
				return sqlc.MonitoringOperation{}, err
			}
			maxAttempts := h.operationMaxAttempts(running.Payload)
			h.recordMonitoringOperationEvent(ctx, running.ID, "info", "queue", "operation execution started", map[string]any{
				"operationType": running.OperationType,
				"targetType":    running.TargetType,
				"targetKey":     running.TargetKey,
				"attemptCount":  running.AttemptCount,
				"maxAttempts":   maxAttempts,
			})
			return running, nil
		},
		Claimed: func(running sqlc.MonitoringOperation) claimedOp {
			maxAttempts := h.operationMaxAttempts(running.Payload)
			return claimedOp{
				ID: running.ID,
				Run: func(ctx context.Context) error {
					return h.executeMonitoringOperation(ctx, running)
				},
				OnComplete: func(ctx context.Context) {
					h.recordMonitoringOperationEvent(ctx, running.ID, "info", "complete", "operation completed", map[string]any{})
					_, _ = h.queries.MarkMonitoringOperationCompleted(ctx, running.ID)
				},
				OnFailure: func(ctx context.Context, err error) {
					h.recordMonitoringOperationEvent(ctx, running.ID, "error", "complete", "operation failed", map[string]any{
						"error": err.Error(),
					})
					_, _ = h.queries.MarkMonitoringOperationFailed(ctx, sqlc.MarkMonitoringOperationFailedParams{ID: running.ID, ErrorMessage: err.Error()})
					if running.AttemptCount < maxAttempts {
						h.recordMonitoringOperationEvent(ctx, running.ID, "warn", "retry", "operation requeued by retry policy", map[string]any{
							"attemptCount": running.AttemptCount,
							"maxAttempts":  maxAttempts,
						})
						_, _ = h.queries.RequeueMonitoringOperation(ctx, running.ID)
					}
					if h.log != nil {
						h.log.Warn("monitoring operation failed", "id", running.ID.String(), "target_type", running.TargetType, "operation_type", running.OperationType, "error", err)
					}
				},
			}
		},
	})
}

func (h *MonitoringHandler) executeMonitoringOperation(ctx context.Context, op sqlc.MonitoringOperation) error {
	var env monitoringOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return err
	}
	switch op.TargetType {
	case "shared_thanos":
		var req SharedThanosStackRequest
		if err := json.Unmarshal(env.Request, &req); err != nil {
			return err
		}
		switch op.OperationType {
		case "install":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Thanos install", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedThanosStack(ctx, protocol.MsgHelmInstall, req, valueOrZeroSecret(env.SecretSpec), env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return err
			}
			return h.verifySharedThanosReadiness(ctx, op.ID, req)
		case "upgrade":
			previousRevision := h.currentReleaseRevision(ctx, req.ManagementClusterID, req.ReleaseName, req.Namespace)
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Thanos upgrade", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedThanosStack(ctx, protocol.MsgHelmUpgrade, req, valueOrZeroSecret(env.SecretSpec), env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			if err := h.verifySharedThanosReadiness(ctx, op.ID, req); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			return nil
		case "replace":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling existing shared Thanos release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "install", "installing replacement shared Thanos release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err = h.applySharedThanosStack(ctx, protocol.MsgHelmInstall, req, valueOrZeroSecret(env.SecretSpec), env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return err
			}
			return h.verifySharedThanosReadiness(ctx, op.ID, req)
		case "uninstall":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling shared Thanos release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			return nil
		}
	case "shared_alertmanager":
		var req SharedAlertmanagerRequest
		if err := json.Unmarshal(env.Request, &req); err != nil {
			return err
		}
		switch op.OperationType {
		case "install":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Alertmanager install", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedAlertmanager(ctx, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return err
			}
			return h.verifySharedAlertmanagerReadiness(ctx, op.ID, req)
		case "upgrade":
			previousRevision := h.currentReleaseRevision(ctx, req.ManagementClusterID, req.ReleaseName, req.Namespace)
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Alertmanager upgrade", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedAlertmanager(ctx, protocol.MsgHelmUpgrade, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			if err := h.verifySharedAlertmanagerReadiness(ctx, op.ID, req); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			return nil
		case "replace":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling existing shared Alertmanager release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "install", "installing replacement shared Alertmanager release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err = h.applySharedAlertmanager(ctx, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return err
			}
			return h.verifySharedAlertmanagerReadiness(ctx, op.ID, req)
		case "uninstall":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling shared Alertmanager release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			return nil
		}
	case "shared_grafana":
		var req SharedGrafanaRequest
		if err := json.Unmarshal(env.Request, &req); err != nil {
			return err
		}
		switch op.OperationType {
		case "install":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Grafana install", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedGrafanaStack(ctx, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return err
			}
			return h.verifySharedGrafanaReadiness(ctx, op.ID, req)
		case "upgrade":
			previousRevision := h.currentReleaseRevision(ctx, req.ManagementClusterID, req.ReleaseName, req.Namespace)
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Grafana upgrade", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedGrafanaStack(ctx, protocol.MsgHelmUpgrade, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			if err := h.verifySharedGrafanaReadiness(ctx, op.ID, req); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			return nil
		case "replace":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling existing shared Grafana release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "install", "installing replacement shared Grafana release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err = h.applySharedGrafanaStack(ctx, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return err
			}
			return h.verifySharedGrafanaReadiness(ctx, op.ID, req)
		case "uninstall":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling shared Grafana release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			// Out-of-band Thanos datasource CM is Helm-adopt annotated, but
			// it is not in the release history until a later Grafana upgrade
			// imports it. Delete it here so a reinstall does not hit
			// "resource already exists and cannot be imported".
			h.deleteGrafanaThanosDatasourceConfigMap(ctx, req)
			return nil
		}
	case "cluster_stack":
		var req MonitoringStackRequest
		if err := json.Unmarshal(env.Request, &req); err != nil {
			return err
		}
		switch op.OperationType {
		case "install":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying cluster monitoring install", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applyMonitoringStack(ctx, env.ClusterID, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, env.ClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return err
			}
			return h.verifyClusterMonitoringReadiness(ctx, op.ID, env.ClusterID, req)
		case "upgrade":
			previousRevision := h.currentReleaseRevision(ctx, env.ClusterID, req.ReleaseName, req.Namespace)
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying cluster monitoring upgrade", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applyMonitoringStack(ctx, env.ClusterID, protocol.MsgHelmUpgrade, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, env.ClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, env.ClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			if err := h.verifyClusterMonitoringReadiness(ctx, op.ID, env.ClusterID, req); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, env.ClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			return nil
		case "replace":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling existing cluster monitoring release", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, env.ClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "install", "installing replacement cluster monitoring release", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err = h.applyMonitoringStack(ctx, env.ClusterID, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, env.ClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return err
			}
			return h.verifyClusterMonitoringReadiness(ctx, op.ID, env.ClusterID, req)
		case "uninstall":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling cluster monitoring release", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, env.ClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 600})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("unsupported monitoring operation: %s/%s", op.TargetType, op.OperationType)
}

func isReleaseMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "release: not found")
}

func valueOrZeroSecret(spec *objectStoreSecretSpec) objectStoreSecretSpec {
	if spec == nil {
		return objectStoreSecretSpec{}
	}
	return *spec
}

func (h *MonitoringHandler) resolveAutoRollbackPolicy(backend sqlc.MonitoringBackend, override *bool) bool {
	if override != nil {
		return *override
	}
	policies := mapFromMapValue(decodeJSONMap(backend.AuthConfig)["operationPolicies"])
	if value, ok := policies["defaultAutoRollbackOnFailure"].(bool); ok {
		return value
	}
	return false
}

func (h *MonitoringHandler) resolveMaxRetryAttempts(backend sqlc.MonitoringBackend) int32 {
	policies := mapFromMapValue(decodeJSONMap(backend.AuthConfig)["operationPolicies"])
	switch value := policies["maxRetryAttempts"].(type) {
	case float64:
		if value >= 1 {
			return int32(value)
		}
	case int32:
		if value >= 1 {
			return value
		}
	case int:
		if value >= 1 {
			return int32(value)
		}
	}
	return 1
}

func (h *MonitoringHandler) operationMaxAttempts(payload json.RawMessage) int32 {
	var env monitoringOperationEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return 1
	}
	if env.ResolvedMaxRetryAttempts < 1 {
		return 1
	}
	return env.ResolvedMaxRetryAttempts
}

func (h *MonitoringHandler) currentReleaseRevision(ctx context.Context, clusterID, releaseName, namespace string) int {
	if h == nil || h.helm == nil || clusterID == "" || releaseName == "" || namespace == "" {
		return 0
	}
	result, err := h.helm.Status(ctx, clusterID, releaseName, namespace)
	if err != nil {
		return 0
	}
	return result.Revision
}

func (h *MonitoringHandler) rollbackIfConfigured(ctx context.Context, operationID uuid.UUID, originalErr error, enabled bool, clusterID, releaseName, namespace string, previousRevision int) error {
	if !enabled {
		return originalErr
	}
	if previousRevision <= 0 {
		return fmt.Errorf("%w; rollback requested but no previous revision was available", originalErr)
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "warn", "rollback", "upgrade failed readiness, attempting rollback", map[string]any{
		"clusterId":        clusterID,
		"releaseName":      releaseName,
		"namespace":        namespace,
		"previousRevision": previousRevision,
		"error":            originalErr.Error(),
	})
	_, rollbackErr := h.helm.Do(ctx, clusterID, protocol.MsgHelmRollback, protocol.HelmRequestPayload{
		ReleaseName: releaseName,
		Namespace:   namespace,
		Revision:    previousRevision,
		Timeout:     900,
	})
	if rollbackErr != nil {
		h.recordMonitoringOperationEvent(ctx, operationID, "error", "rollback", "rollback failed", map[string]any{
			"clusterId":        clusterID,
			"releaseName":      releaseName,
			"namespace":        namespace,
			"previousRevision": previousRevision,
			"error":            rollbackErr.Error(),
		})
		return fmt.Errorf("%w; rollback to revision %d failed: %v", originalErr, previousRevision, rollbackErr)
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "rollback", "rollback completed", map[string]any{
		"clusterId":        clusterID,
		"releaseName":      releaseName,
		"namespace":        namespace,
		"previousRevision": previousRevision,
	})
	if err := h.waitForReleaseReadiness(ctx, operationID, clusterID, namespace, releaseName, 1, 2*time.Minute); err != nil {
		return fmt.Errorf("%w; rollback succeeded but readiness after rollback failed: %v", originalErr, err)
	}
	return fmt.Errorf("%w; rollback to revision %d completed", originalErr, previousRevision)
}

func (h *MonitoringHandler) recordMonitoringOperationEvent(ctx context.Context, operationID uuid.UUID, level, stage, message string, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, _ = h.queries.CreateMonitoringOperationEvent(ctx, sqlc.CreateMonitoringOperationEventParams{
		OperationID: operationID,
		Level:       level,
		Stage:       stage,
		Message:     message,
		Detail:      raw,
	})
}

func (h *MonitoringHandler) waitForReleaseReadiness(ctx context.Context, operationID uuid.UUID, clusterID, namespace, releaseName string, minReadyPods int, timeout time.Duration) error {
	if h.requester == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	labelSelector := url.QueryEscape("app.kubernetes.io/instance=" + releaseName)
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "readiness", "waiting for release readiness", map[string]any{
		"clusterId":    clusterID,
		"namespace":    namespace,
		"releaseName":  releaseName,
		"minReadyPods": minReadyPods,
		"timeout":      timeout.String(),
	})
	for {
		ready, total, err := h.countReadyReleasePods(ctx, clusterID, namespace, labelSelector)
		if err == nil && total >= minReadyPods && ready >= minReadyPods {
			h.recordMonitoringOperationEvent(ctx, operationID, "info", "readiness", "release became ready", map[string]any{
				"clusterId":   clusterID,
				"namespace":   namespace,
				"releaseName": releaseName,
				"readyPods":   ready,
				"totalPods":   total,
			})
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("release readiness check timed out: %w", err)
			}
			return fmt.Errorf("release readiness check timed out: %d/%d ready pods for %s", ready, total, releaseName)
		}
		if err != nil {
			h.recordMonitoringOperationEvent(ctx, operationID, "warn", "readiness", "release readiness poll failed", map[string]any{
				"error": err.Error(),
			})
		}
		time.Sleep(5 * time.Second)
	}
}

func (h *MonitoringHandler) countReadyReleasePods(ctx context.Context, clusterID, namespace, labelSelector string) (int, int, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods?labelSelector=%s", namespace, labelSelector)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return 0, 0, err
	}
	if err := ensureSuccess(resp); err != nil {
		return 0, 0, err
	}
	var payload map[string]any
	if err := parseJSONResponse(resp, &payload); err != nil {
		return 0, 0, err
	}
	items := objectItems(payload)
	ready := 0
	for _, item := range items {
		if podReady(item) {
			ready++
		}
	}
	return ready, len(items), nil
}

func podReady(item map[string]any) bool {
	status, _ := item["status"].(map[string]any)
	phase, _ := status["phase"].(string)
	if phase != "Running" {
		return false
	}
	conditions, _ := status["conditions"].([]any)
	for _, cond := range conditions {
		entry, _ := cond.(map[string]any)
		if entry == nil {
			continue
		}
		if entry["type"] == "Ready" && entry["status"] == "True" {
			return true
		}
	}
	return false
}

func (h *MonitoringHandler) verifySharedThanosReadiness(ctx context.Context, operationID uuid.UUID, req SharedThanosStackRequest) error {
	serviceName := defaultString(req.ReleaseName, "thanos") + "-query-frontend"
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "service", "checking shared Thanos query frontend health", map[string]any{
		"service":   serviceName,
		"namespace": req.Namespace,
	})
	if err := h.waitForServiceProxySuccess(ctx, req.ManagementClusterID, req.Namespace, serviceName, "9090", "/-/healthy", "", 90*time.Second); err != nil {
		return err
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "smoke", "running shared Thanos PromQL smoke query", map[string]any{
		"service": serviceName,
	})
	if err := h.waitForServiceProxySuccess(ctx, req.ManagementClusterID, req.Namespace, serviceName, "9090", "/api/v1/query", "query=vector(1)", 90*time.Second); err != nil {
		return err
	}
	if err := h.syncGrafanaThanosDatasource(ctx); err != nil && h.log != nil {
		h.log.Warn("sync grafana thanos datasource after Thanos readiness", "error", err)
	}
	return nil
}

func (h *MonitoringHandler) verifySharedAlertmanagerReadiness(ctx context.Context, operationID uuid.UUID, req SharedAlertmanagerRequest) error {
	serviceName := defaultString(req.ReleaseName, "astronomer-alertmanager")
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "service", "checking shared Alertmanager health", map[string]any{
		"service":   serviceName,
		"namespace": req.Namespace,
	})
	return h.waitForServiceProxySuccess(ctx, req.ManagementClusterID, req.Namespace, serviceName, "9093", "/-/healthy", "", 90*time.Second)
}

func (h *MonitoringHandler) verifySharedGrafanaReadiness(ctx context.Context, operationID uuid.UUID, req SharedGrafanaRequest) error {
	serviceName := defaultString(req.ReleaseName, sharedGrafanaDefaultRelease)
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "service", "checking shared Grafana health", map[string]any{
		"service":   serviceName,
		"namespace": req.Namespace,
	})
	if err := h.waitForServiceProxySuccess(ctx, req.ManagementClusterID, req.Namespace, serviceName, "80", "/api/health", "", 90*time.Second); err != nil {
		return err
	}
	return h.stampSharedGrafanaHealth(ctx, req)
}

func (h *MonitoringHandler) verifyClusterMonitoringReadiness(ctx context.Context, operationID uuid.UUID, clusterID string, req MonitoringStackRequest) error {
	serviceName, err := h.findPrometheusServiceName(ctx, clusterID, req.Namespace, req.ReleaseName)
	if err != nil {
		return err
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "service", "checking cluster Prometheus service health", map[string]any{
		"service":   serviceName,
		"namespace": req.Namespace,
	})
	if err := h.waitForServiceProxySuccess(ctx, clusterID, req.Namespace, serviceName, "9090", "/-/healthy", "", 90*time.Second); err != nil {
		return err
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "smoke", "running cluster Prometheus PromQL smoke query", map[string]any{
		"service": serviceName,
	})
	return h.waitForServiceProxySuccess(ctx, clusterID, req.Namespace, serviceName, "9090", "/api/v1/query", "query=vector(1)", 90*time.Second)
}

func (h *MonitoringHandler) waitForServiceProxySuccess(ctx context.Context, clusterID, namespace, serviceName, port, path, rawQuery string, timeout time.Duration) error {
	if h.requester == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		lastErr = h.serviceProxyCheck(ctx, clusterID, namespace, serviceName, port, path, rawQuery)
		if lastErr == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("service readiness check timed out for %s/%s: %w", namespace, serviceName, lastErr)
		}
		time.Sleep(5 * time.Second)
	}
}

func (h *MonitoringHandler) serviceProxyCheck(ctx context.Context, clusterID, namespace, serviceName, port, path, rawQuery string) error {
	target := serviceName + ":" + port
	proxyPath := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s/proxy%s", namespace, target, path)
	if rawQuery != "" {
		proxyPath += "?" + rawQuery
	}
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, proxyPath, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	return ensureSuccess(resp)
}

func (h *MonitoringHandler) findPrometheusServiceName(ctx context.Context, clusterID, namespace, releaseName string) (string, error) {
	if h.requester == nil {
		return "", fmt.Errorf("kubernetes requester not configured")
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/services?labelSelector=%s", namespace, url.QueryEscape("app.kubernetes.io/instance="+releaseName))
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return "", err
	}
	if err := ensureSuccess(resp); err != nil {
		return "", err
	}
	var payload map[string]any
	if err := parseJSONResponse(resp, &payload); err != nil {
		return "", err
	}
	for _, item := range objectItems(payload) {
		meta, _ := item["metadata"].(map[string]any)
		spec, _ := item["spec"].(map[string]any)
		name, _ := meta["name"].(string)
		if name == "" {
			continue
		}
		if strings.Contains(strings.ToLower(name), "prometheus") && serviceExposesPort(spec, 9090) {
			return name, nil
		}
	}
	return "", fmt.Errorf("prometheus service not found for release %s", releaseName)
}

func serviceExposesPort(spec map[string]any, port int) bool {
	ports, _ := spec["ports"].([]any)
	for _, item := range ports {
		entry, _ := item.(map[string]any)
		if entry == nil {
			continue
		}
		if intValue(entry, "port") == port || intValue(entry, "targetPort") == port {
			return true
		}
	}
	return false
}
