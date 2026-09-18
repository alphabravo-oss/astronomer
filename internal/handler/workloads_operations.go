package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type idempotentWorkloadOperationCreator interface {
	CreateWorkloadOperationIdempotent(context.Context, sqlc.CreateWorkloadOperationIdempotentParams) (sqlc.WorkloadOperation, error)
}

var errWorkloadOperationIdempotencyConflict = errors.New("workload operation idempotency key identifies a different operation")

func respondWorkloadMutationError(w http.ResponseWriter, r *http.Request, err error, fallbackCode, fallbackMessage string) {
	if errors.Is(err, errWorkloadOperationIdempotencyConflict) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different workload operation")
		return
	}
	respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, fallbackCode, fallbackMessage)
}

func (h *WorkloadHandler) createAuditedWorkloadOperation(r *http.Request, targetType, targetKey, operationType string, env workloadOperationEnvelope, userID pgtype.UUID, event mutationAuditEvent) (sqlc.WorkloadOperation, error) {
	opContext := withOperationIdempotency(r, "workloads")
	op, err := executeMutation(r, h.runTx,
		func(q WorkloadMutationTx) (sqlc.WorkloadOperation, error) {
			return createWorkloadOperation(opContext, q, targetType, targetKey, operationType, env, userID)
		},
		func(op sqlc.WorkloadOperation) mutationAuditEvent {
			detail := make(map[string]any, len(event.detail)+1)
			for key, value := range event.detail {
				detail[key] = value
			}
			detail["operation_id"] = op.ID.String()
			event.detail = detail
			return event
		})
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

func createWorkloadOperation(ctx context.Context, q workloadOperationCreator, targetType, targetKey, operationType string, env workloadOperationEnvelope, userID pgtype.UUID) (sqlc.WorkloadOperation, error) {
	payload, err := json.Marshal(env)
	if err != nil {
		return sqlc.WorkloadOperation{}, err
	}
	params := sqlc.CreateWorkloadOperationParams{
		TargetType:    targetType,
		TargetKey:     targetKey,
		OperationType: operationType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	}
	var op sqlc.WorkloadOperation
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		if creator, ok := q.(idempotentWorkloadOperationCreator); ok {
			op, err = creator.CreateWorkloadOperationIdempotent(ctx, sqlc.CreateWorkloadOperationIdempotentParams{
				Scope:          idem.scope,
				IdempotencyKey: idem.key,
				TargetType:     params.TargetType,
				TargetKey:      params.TargetKey,
				OperationType:  params.OperationType,
				Payload:        params.Payload,
				Status:         params.Status,
				CreatedByID:    params.CreatedByID,
			})
			if err == nil && op.ID != uuid.Nil && (op.TargetType != params.TargetType || op.TargetKey != params.TargetKey || op.OperationType != params.OperationType || !jsonPayloadEqual(op.Payload, params.Payload)) {
				return sqlc.WorkloadOperation{}, errWorkloadOperationIdempotencyConflict
			}
		}
	}
	if op.ID == uuid.Nil && err == nil {
		op, err = q.CreateWorkloadOperation(ctx, params)
	}
	return op, err
}

func workloadOperationResponse(op sqlc.WorkloadOperation) map[string]any {
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

func workloadOperationEventsResponse(events []sqlc.WorkloadOperationEvent) []map[string]any {
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

func (h *WorkloadHandler) processPendingOperations(ctx context.Context) {
	// Claim under the lock, dispatch outside — one slow cluster must
	// not block other clusters' workload operations. Same shape as the
	// catalog/tools/monitoring reconcilers.
	dispatchClaimed(ctx, h.helmConcurrency, h.claimPendingWorkloadOperations(ctx))
}

func (h *WorkloadHandler) claimPendingWorkloadOperations(ctx context.Context) []claimedOp {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListPendingWorkloadOperations(ctx, 20)
	if err != nil {
		return nil
	}
	return claimLatestOperations(ctx, ops, operationRunnerConfig[sqlc.WorkloadOperation]{
		ID:        func(op sqlc.WorkloadOperation) uuid.UUID { return op.ID },
		TargetKey: func(op sqlc.WorkloadOperation) string { return op.TargetType + ":" + op.TargetKey },
		Status:    func(op sqlc.WorkloadOperation) string { return op.Status },
		IsFreshRunning: func(op sqlc.WorkloadOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) < time.Minute
		},
		Supersede: func(ctx context.Context, op sqlc.WorkloadOperation) {
			h.recordOperationEvent(ctx, op.ID, "info", "queue", "operation superseded by newer desired state", map[string]any{"targetKey": op.TargetKey})
			_, _ = h.queries.MarkWorkloadOperationSuperseded(ctx, sqlc.MarkWorkloadOperationSupersededParams{ID: op.ID, ErrorMessage: operationSupersededMessage})
		},
		MarkRunning: func(ctx context.Context, op sqlc.WorkloadOperation) (sqlc.WorkloadOperation, error) {
			running, err := h.queries.MarkWorkloadOperationRunning(ctx, op.ID)
			if err != nil {
				return sqlc.WorkloadOperation{}, err
			}
			h.recordOperationEvent(ctx, running.ID, "info", "queue", "operation execution started", map[string]any{"operationType": running.OperationType, "targetKey": running.TargetKey})
			return running, nil
		},
		Claimed: func(running sqlc.WorkloadOperation) claimedOp {
			return claimedOp{
				ID: running.ID,
				Run: func(ctx context.Context) error {
					return h.executeOperation(ctx, running)
				},
				OnComplete: func(ctx context.Context) {
					if _, err := h.queries.MarkWorkloadOperationCompleted(ctx, sqlc.MarkWorkloadOperationCompletedParams{
						ID: running.ID, AttemptCount: running.AttemptCount,
					}); err == nil {
						h.recordOperationEvent(ctx, running.ID, "info", "complete", "operation completed", map[string]any{})
					}
				},
				OnFailure: func(ctx context.Context, err error) {
					if _, persistErr := h.queries.MarkWorkloadOperationFailed(ctx, sqlc.MarkWorkloadOperationFailedParams{
						ID: running.ID, AttemptCount: running.AttemptCount, ErrorMessage: err.Error(),
					}); persistErr == nil {
						h.recordOperationEvent(ctx, running.ID, "error", "complete", "operation failed", map[string]any{"error": err.Error()})
					}
				},
			}
		},
	})
}

func (h *WorkloadHandler) executeOperation(ctx context.Context, op sqlc.WorkloadOperation) error {
	var env workloadOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return err
	}
	switch op.OperationType {
	case "scale":
		payload, _ := json.Marshal(map[string]any{"spec": map[string]any{"replicas": env.Replicas}})
		path, err := scalePath(env.Kind, env.Namespace, env.Name)
		if err != nil {
			return err
		}
		h.recordOperationEvent(ctx, op.ID, "info", "scale", "scaling workload", map[string]any{"replicas": env.Replicas})
		resp, err := h.requester.Do(ctx, env.ClusterID, http.MethodPatch, path, payload, requestHeaders("application/merge-patch+json"))
		if err != nil {
			return err
		}
		return ensureSuccess(resp)
	case "restart":
		payload, _ := json.Marshal(map[string]any{
			"spec": map[string]any{
				"template": map[string]any{
					"metadata": map[string]any{
						"annotations": map[string]string{
							"kubectl.kubernetes.io/restartedAt": time.Now().UTC().Format(time.RFC3339),
						},
					},
				},
			},
		})
		path, err := workloadPath(env.Kind, env.Namespace, env.Name)
		if err != nil {
			return err
		}
		h.recordOperationEvent(ctx, op.ID, "info", "restart", "restarting workload", map[string]any{})
		resp, err := h.requester.Do(ctx, env.ClusterID, http.MethodPatch, path, payload, requestHeaders("application/merge-patch+json"))
		if err != nil {
			return err
		}
		return ensureSuccess(resp)
	case "delete":
		path, err := workloadPath(env.Kind, env.Namespace, env.Name)
		if err != nil {
			return err
		}
		h.recordOperationEvent(ctx, op.ID, "info", "delete", "deleting workload", map[string]any{})
		resp, err := h.requester.Do(ctx, env.ClusterID, http.MethodDelete, path, nil, requestHeaders(""))
		if err != nil {
			return err
		}
		return ensureSuccess(resp)
	default:
		return fmt.Errorf("unsupported workload operation type: %s", op.OperationType)
	}
}

func (h *WorkloadHandler) recordOperationEvent(ctx context.Context, operationID uuid.UUID, level, stage, message string, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, _ = h.queries.CreateWorkloadOperationEvent(ctx, sqlc.CreateWorkloadOperationEventParams{
		OperationID: operationID,
		Level:       level,
		Stage:       stage,
		Message:     message,
		Detail:      raw,
	})
}

func workloadTargetKey(clusterID, kind, namespace, name string) string {
	return clusterID + ":" + kind + ":" + namespace + ":" + name
}
