package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var errMonitoringOperationNotRetryable = errors.New("monitoring operation is not retryable")

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
	go h.RunReconciler(ctx)
}

// RunReconciler blocks until ctx is cancelled and joins all monitoring-owned
// loops before returning. Production uses this form so the server supervisor
// has one completion signal for the complete monitoring runtime.
func (h *MonitoringHandler) RunReconciler(ctx context.Context) {
	if h == nil || h.queries == nil || h.helm == nil {
		return
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	var loops sync.WaitGroup
	for _, run := range []func(context.Context){h.runReconciler, h.runLokiIngestReconciler, h.runGrafanaFolderReconciler} {
		loops.Add(1)
		go func() {
			defer loops.Done()
			run(ctx)
		}()
	}
	loops.Wait()
}

func (h *MonitoringHandler) ListOperations(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		paging.Write(w, []any{}, paging.Exact(0, queryLimit(r, 50), queryOffset(r), 0))
		return
	}
	limit := int32(queryLimit(r, 50))
	offset := int32(queryOffset(r))
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
	paging.Write(w, resp, paging.FromPage(int(limit), int(offset), len(items)))
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
	if !h.authorizeMonitoringOperationUpdate(w, r, op) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "monitoring transaction runner is not configured")
		return
	}
	r = r.WithContext(withOperationIdempotency(r, "monitoring_operation_retry"))
	digest, err := canonicalOperationRequestDigest(struct {
		Action      string `json:"action"`
		OperationID string `json:"operation_id"`
	}{Action: "retry", OperationID: id.String()})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode monitoring retry request")
		return
	}
	var receipt map[string]any
	replayed := false
	err = h.runTx(r.Context(), func(q MonitoringMutationTx) error {
		idemQ, ok := q.(resourceOperationIdempotencyQuerier)
		if !ok {
			return errors.New("monitoring retry idempotency store is not configured")
		}
		_, stored, replay, claimErr := claimOperationReceipt[map[string]any](r.Context(), idemQ, "monitoring_operation_retries", digest)
		if claimErr != nil {
			return claimErr
		}
		if replay {
			receipt, replayed = stored, true
			return nil
		}
		if !isRetryableOperationStatus(op.Status) {
			return errMonitoringOperationNotRetryable
		}
		requeued, requeueErr := q.RequeueMonitoringOperation(r.Context(), id)
		if requeueErr != nil {
			return requeueErr
		}
		receipt = monitoringOperationResponse(requeued)
		if auditErr := recordAuditOutbox(r, q, "monitoring.operation.retry", "monitoring_operation", id.String(), op.TargetKey, http.StatusAccepted, map[string]any{
			"target_type": op.TargetType, "previous_status": op.Status,
		}); auditErr != nil {
			return auditErr
		}
		return attachOperationReceipt(r.Context(), idemQ, "monitoring_operation_retries", requeued.ID, digest, receipt)
	})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different monitoring operation retry")
			return
		}
		if errors.Is(err, errMonitoringOperationNotRetryable) {
			requireRetryableOperation(w, r, op.Status)
			return
		}
		respondMonitoringMutationError(w, r, err, http.StatusInternalServerError, apierror.MonitoringError, "Failed to requeue monitoring operation")
		return
	}
	if !replayed {
		h.TriggerReconcile()
	}
	RespondAcceptedOperation(w, "/api/v1/settings/monitoring/operations/"+id.String()+"/", receipt)
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

func (h *MonitoringHandler) enqueueSharedThanosOperationWith(ctx context.Context, q monitoringSharedMutationWriter, userID pgtype.UUID, opType string, req SharedThanosStackRequest, values map[string]any, secretSpec *objectStoreSecretSpec) (sqlc.MonitoringOperation, error) {
	return enqueueSharedMonitoringOperationWith(ctx, h, q, userID, opType, "shared_thanos", req.ManagementClusterID, req, values, secretSpec, req.AutoRollbackOnFailure)
}

func (h *MonitoringHandler) enqueueSharedLokiOperationWith(ctx context.Context, q monitoringSharedMutationWriter, userID pgtype.UUID, opType string, req SharedLokiRequest, values map[string]any) (sqlc.MonitoringOperation, error) {
	return enqueueSharedMonitoringOperationWith(ctx, h, q, userID, opType, "shared_loki", req.ManagementClusterID, req, values, nil, req.AutoRollbackOnFailure)
}

func (h *MonitoringHandler) enqueueSharedGrafanaOperationWith(ctx context.Context, q monitoringSharedMutationWriter, userID pgtype.UUID, opType string, req SharedGrafanaRequest, values map[string]any) (sqlc.MonitoringOperation, error) {
	return enqueueSharedMonitoringOperationWith(ctx, h, q, userID, opType, "shared_grafana", req.ManagementClusterID, req, values, nil, req.AutoRollbackOnFailure)
}

func (h *MonitoringHandler) enqueueSharedAlertmanagerOperationWith(ctx context.Context, q monitoringSharedMutationWriter, userID pgtype.UUID, opType string, req SharedAlertmanagerRequest, values map[string]any) (sqlc.MonitoringOperation, error) {
	return enqueueSharedMonitoringOperationWith(ctx, h, q, userID, opType, "shared_alertmanager", req.ManagementClusterID, req, values, nil, req.AutoRollbackOnFailure)
}

func enqueueSharedMonitoringOperationWith[Req any](ctx context.Context, h *MonitoringHandler, q monitoringSharedMutationWriter, userID pgtype.UUID, opType, targetType, clusterID string, req Req, values map[string]any, secretSpec *objectStoreSecretSpec, rollbackOverride *bool) (sqlc.MonitoringOperation, error) {
	rawReq, err := json.Marshal(req)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	backend, err := q.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	payload, err := json.Marshal(monitoringOperationEnvelope{
		ClusterID:                clusterID,
		Request:                  rawReq,
		Values:                   values,
		SecretSpec:               secretSpec,
		ResolvedAutoRollback:     h.resolveAutoRollbackPolicy(backend, rollbackOverride),
		ResolvedMaxRetryAttempts: h.resolveMaxRetryAttempts(backend),
	})
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	return createMonitoringOperationWith(ctx, q, sqlc.CreateMonitoringOperationParams{
		TargetType:    targetType,
		TargetKey:     "shared",
		OperationType: opType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	})
}
