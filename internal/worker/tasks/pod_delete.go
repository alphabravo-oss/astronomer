package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const PodDeleteType = "pod:delete"

type PodDeletePayload struct {
	OperationID string `json:"operation_id"`
}

type podDeleteOperationEnvelope struct {
	ClusterID string `json:"clusterId"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func NewPodDeleteTask(operationID uuid.UUID) (*asynq.Task, error) {
	if operationID == uuid.Nil {
		return nil, errors.New("pod delete requires operation_id")
	}
	body, err := json.Marshal(PodDeletePayload{OperationID: operationID.String()})
	if err != nil {
		return nil, fmt.Errorf("marshal pod delete: %w", err)
	}
	return asynq.NewTask(PodDeleteType, body, asynq.MaxRetry(5), asynq.Timeout(2*time.Minute)), nil
}

type PodDeleteQuerier interface {
	ClaimPodDeleteOperation(context.Context, uuid.UUID) (sqlc.WorkloadOperation, error)
	GetWorkloadOperation(context.Context, uuid.UUID) (sqlc.WorkloadOperation, error)
	MarkWorkloadOperationCompleted(context.Context, sqlc.MarkWorkloadOperationCompletedParams) (sqlc.WorkloadOperation, error)
	MarkWorkloadOperationFailed(context.Context, sqlc.MarkWorkloadOperationFailedParams) (sqlc.WorkloadOperation, error)
	MarkWorkloadOperationRetrying(context.Context, sqlc.MarkWorkloadOperationRetryingParams) (sqlc.WorkloadOperation, error)
	CreateWorkloadOperationEvent(context.Context, sqlc.CreateWorkloadOperationEventParams) (sqlc.WorkloadOperationEvent, error)
}

// HandlePodDelete applies a committed operation through the tunnel. DELETE is
// naturally convergent: Kubernetes 404 means the requested absent state has
// already been reached, so retry after an uncertain response is safe.
func HandlePodDelete(ctx context.Context, task *asynq.Task) error {
	if task == nil {
		return asynq.SkipRetry
	}
	var payload PodDeletePayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: decode pod delete: %v", asynq.SkipRetry, err)
	}
	operationID, err := uuid.Parse(payload.OperationID)
	if err != nil {
		return fmt.Errorf("%w: invalid pod delete operation ID", asynq.SkipRetry)
	}
	deps := runtimeDependencies(ctx)
	queries, ok := deps.Queries.(PodDeleteQuerier)
	if !ok || queries == nil || deps.K8s == nil {
		return errors.New("pod delete runtime is not configured")
	}

	operation, err := queries.ClaimPodDeleteOperation(ctx, operationID)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetWorkloadOperation(ctx, operationID)
		if errors.Is(loadErr, pgx.ErrNoRows) {
			return nil
		}
		if loadErr != nil {
			return fmt.Errorf("load pod delete operation: %w", loadErr)
		}
		if existing.OperationType != "delete_pod" || existing.TargetType != "pod" {
			return fmt.Errorf("%w: operation is not a pod delete", asynq.SkipRetry)
		}
		if existing.Status == "completed" {
			return nil
		}
		return errors.New("pod delete operation is already claimed")
	}
	if err != nil {
		return fmt.Errorf("claim pod delete operation: %w", err)
	}
	if operation.TargetType != "pod" || operation.OperationType != "delete_pod" {
		return markPodDeleteFailed(ctx, queries, operation, "invalid_target",
			fmt.Errorf("%w: pod delete operation target is invalid", asynq.SkipRetry))
	}
	var envelope podDeleteOperationEnvelope
	if err := json.Unmarshal(operation.Payload, &envelope); err != nil {
		return markPodDeleteFailed(ctx, queries, operation, "invalid_intent",
			fmt.Errorf("%w: invalid pod delete operation envelope", asynq.SkipRetry))
	}
	clusterID, err := uuid.Parse(envelope.ClusterID)
	if err != nil || !strings.EqualFold(envelope.Kind, "Pod") || envelope.Namespace == "" || envelope.Name == "" {
		return markPodDeleteFailed(ctx, queries, operation, "invalid_target",
			fmt.Errorf("%w: invalid pod delete target", asynq.SkipRetry))
	}
	path := "/api/v1/namespaces/" + url.PathEscape(envelope.Namespace) + "/pods/" + url.PathEscape(envelope.Name)
	response, effectErr := deps.K8s.Do(ctx, clusterID.String(), http.MethodDelete, path, nil, nil)
	if effectErr != nil {
		category, terminal := classifyPodDeleteError(effectErr, 0)
		runtimeLogger(ctx).WarnContext(ctx, "pod delete failed", "operation_id", operation.ID.String(),
			"cluster_id", clusterID.String(), "category", category)
		cause := errors.New("pod delete failed: " + category)
		if terminal {
			cause = fmt.Errorf("%w: %v", asynq.SkipRetry, cause)
		}
		return markPodDeleteFailed(ctx, queries, operation, category, cause)
	}
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	if response == nil || (status >= http.StatusBadRequest && status != http.StatusNotFound) {
		category, terminal := classifyPodDeleteError(nil, status)
		cause := errors.New("pod delete failed: " + category)
		if terminal {
			cause = fmt.Errorf("%w: %v", asynq.SkipRetry, cause)
		}
		return markPodDeleteFailed(ctx, queries, operation, category, cause)
	}
	if _, err := queries.MarkWorkloadOperationCompleted(ctx, sqlc.MarkWorkloadOperationCompletedParams{
		ID: operation.ID, AttemptCount: operation.AttemptCount,
	}); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return fmt.Errorf("mark pod delete completed: %w", err)
	}
	detail, _ := json.Marshal(map[string]any{"convergence": "absent", "outcome": "completed"})
	if _, err := queries.CreateWorkloadOperationEvent(ctx, sqlc.CreateWorkloadOperationEventParams{
		OperationID: operation.ID, Level: "info", Stage: "completed",
		Message: "Pod delete completed", Detail: detail,
	}); err != nil {
		return fmt.Errorf("record pod delete outcome: %w", err)
	}
	return nil
}

func markPodDeleteFailed(ctx context.Context, queries PodDeleteQuerier, operation sqlc.WorkloadOperation, category string, cause error) error {
	terminal := errors.Is(cause, asynq.SkipRetry) || workloadTaskRetriesExhausted(ctx)
	status, stage, message := "retrying", "retrying", "Pod delete will retry"
	var persistErr error
	if terminal {
		status, stage, message = "failed", "failed", "Pod delete failed"
		_, persistErr = queries.MarkWorkloadOperationFailed(ctx, sqlc.MarkWorkloadOperationFailedParams{
			ID: operation.ID, AttemptCount: operation.AttemptCount, ErrorMessage: "pod delete failed: " + category,
		})
	} else {
		_, persistErr = queries.MarkWorkloadOperationRetrying(ctx, sqlc.MarkWorkloadOperationRetryingParams{
			ID: operation.ID, AttemptCount: operation.AttemptCount, ErrorMessage: "pod delete failed: " + category,
		})
	}
	if errors.Is(persistErr, pgx.ErrNoRows) {
		return nil
	}
	if persistErr != nil {
		return errors.Join(cause, fmt.Errorf("mark pod delete failed: %w", persistErr))
	}
	detail, _ := json.Marshal(map[string]any{"category": category, "outcome": status})
	if _, err := queries.CreateWorkloadOperationEvent(ctx, sqlc.CreateWorkloadOperationEventParams{
		OperationID: operation.ID, Level: "error", Stage: stage,
		Message: message, Detail: detail,
	}); err != nil {
		return errors.Join(cause, fmt.Errorf("record pod delete failure: %w", err))
	}
	if terminal && !errors.Is(cause, asynq.SkipRetry) {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, cause)
	}
	return cause
}

func classifyPodDeleteError(err error, status int) (string, bool) {
	if status == http.StatusForbidden || status == http.StatusUnauthorized {
		return "authorization_denied", true
	}
	if status == http.StatusConflict {
		return "resource_conflict", false
	}
	if status == http.StatusTooManyRequests {
		return "kubernetes_throttled", false
	}
	if status >= http.StatusBadRequest {
		return "kubernetes_http_error", status < http.StatusInternalServerError
	}
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return "tunnel_timeout", false
	}
	return "tunnel_unreachable", false
}
