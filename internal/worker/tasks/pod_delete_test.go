package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type podDeleteQueriesFake struct {
	RuntimeQuerier
	operation sqlc.WorkloadOperation
	events    []sqlc.CreateWorkloadOperationEventParams
}

func (f *podDeleteQueriesFake) ClaimPodDeleteOperation(_ context.Context, id uuid.UUID) (sqlc.WorkloadOperation, error) {
	if f.operation.ID != id || f.operation.Status == "completed" || f.operation.Status == "running" {
		return sqlc.WorkloadOperation{}, pgx.ErrNoRows
	}
	f.operation.Status = "running"
	f.operation.AttemptCount++
	return f.operation, nil
}

func (f *podDeleteQueriesFake) GetWorkloadOperation(_ context.Context, id uuid.UUID) (sqlc.WorkloadOperation, error) {
	if f.operation.ID != id {
		return sqlc.WorkloadOperation{}, pgx.ErrNoRows
	}
	return f.operation, nil
}

func (f *podDeleteQueriesFake) MarkWorkloadOperationCompleted(_ context.Context, arg sqlc.MarkWorkloadOperationCompletedParams) (sqlc.WorkloadOperation, error) {
	if f.operation.ID != arg.ID || f.operation.AttemptCount != arg.AttemptCount || f.operation.Status != "running" {
		return sqlc.WorkloadOperation{}, pgx.ErrNoRows
	}
	f.operation.Status = "completed"
	return f.operation, nil
}

func (f *podDeleteQueriesFake) MarkWorkloadOperationFailed(_ context.Context, arg sqlc.MarkWorkloadOperationFailedParams) (sqlc.WorkloadOperation, error) {
	if f.operation.ID != arg.ID || f.operation.AttemptCount != arg.AttemptCount || f.operation.Status != "running" {
		return sqlc.WorkloadOperation{}, pgx.ErrNoRows
	}
	f.operation.Status = "failed"
	f.operation.ErrorMessage = arg.ErrorMessage
	return f.operation, nil
}

func (f *podDeleteQueriesFake) MarkWorkloadOperationRetrying(_ context.Context, arg sqlc.MarkWorkloadOperationRetryingParams) (sqlc.WorkloadOperation, error) {
	if f.operation.ID != arg.ID || f.operation.AttemptCount != arg.AttemptCount || f.operation.Status != "running" {
		return sqlc.WorkloadOperation{}, pgx.ErrNoRows
	}
	f.operation.Status = "retrying"
	f.operation.ErrorMessage = arg.ErrorMessage
	return f.operation, nil
}

func (f *podDeleteQueriesFake) CreateWorkloadOperationEvent(_ context.Context, arg sqlc.CreateWorkloadOperationEventParams) (sqlc.WorkloadOperationEvent, error) {
	f.events = append(f.events, arg)
	return sqlc.WorkloadOperationEvent{ID: uuid.New(), OperationID: arg.OperationID, Detail: arg.Detail}, nil
}

type podDeleteK8sFake struct {
	status int
	err    error
	onDo   func()
	calls  []struct{ cluster, method, path string }
}

func (f *podDeleteK8sFake) Do(_ context.Context, cluster, method, path string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	f.calls = append(f.calls, struct{ cluster, method, path string }{cluster, method, path})
	if f.onDo != nil {
		f.onDo()
	}
	if f.err != nil {
		return nil, f.err
	}
	return &protocol.K8sResponsePayload{StatusCode: f.status}, nil
}

func podDeleteOperation(t *testing.T) sqlc.WorkloadOperation {
	t.Helper()
	clusterID := uuid.NewString()
	payload, err := json.Marshal(podDeleteOperationEnvelope{ClusterID: clusterID, Kind: "Pod", Namespace: "payments", Name: "api-0"})
	if err != nil {
		t.Fatal(err)
	}
	return sqlc.WorkloadOperation{ID: uuid.New(), TargetType: "pod", TargetKey: clusterID + ":Pod:payments:api-0",
		OperationType: "delete_pod", Payload: payload, Status: "pending"}
}

func TestHandlePodDeleteConvergesAndCompletedReplayIsNoOp(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			operation := podDeleteOperation(t)
			queries := &podDeleteQueriesFake{operation: operation}
			k8s := &podDeleteK8sFake{status: status}
			ctx := testRuntimeContext(RuntimeDependencies{Queries: queries, K8s: k8s})
			task, err := NewPodDeleteTask(operation.ID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(task.Payload()), "payments") || strings.Contains(string(task.Payload()), operation.TargetKey) {
				t.Fatalf("task is not identifier-only: %s", task.Payload())
			}
			if err := HandlePodDelete(ctx, task); err != nil {
				t.Fatalf("handle pod delete: %v", err)
			}
			if queries.operation.Status != "completed" || len(k8s.calls) != 1 || k8s.calls[0].method != http.MethodDelete {
				t.Fatalf("operation=%+v calls=%+v", queries.operation, k8s.calls)
			}
			if len(queries.events) != 1 || strings.Contains(string(queries.events[0].Detail), "payments") {
				t.Fatalf("outcome evidence is not sanitized: %+v", queries.events)
			}
			if err := HandlePodDelete(ctx, task); err != nil || len(k8s.calls) != 1 {
				t.Fatalf("completed replay repeated effect: err=%v calls=%+v", err, k8s.calls)
			}
		})
	}
}

func TestHandlePodDeletePersistsOnlySanitizedFailure(t *testing.T) {
	operation := podDeleteOperation(t)
	queries := &podDeleteQueriesFake{operation: operation}
	k8s := &podDeleteK8sFake{err: errors.New("timeout Authorization: Bearer TOP-SECRET")}
	ctx := testRuntimeContext(RuntimeDependencies{Queries: queries, K8s: k8s})
	task, _ := NewPodDeleteTask(operation.ID)
	err := HandlePodDelete(ctx, task)
	if err == nil || strings.Contains(err.Error(), "TOP-SECRET") {
		t.Fatalf("retry error=%v", err)
	}
	persisted, _ := json.Marshal(map[string]any{"error": queries.operation.ErrorMessage, "events": queries.events})
	if strings.Contains(string(persisted), "TOP-SECRET") || queries.operation.ErrorMessage != "pod delete failed: tunnel_timeout" {
		t.Fatalf("persisted evidence=%s", persisted)
	}
}

func TestHandlePodDeleteMarksClaimedInvalidTargetTerminal(t *testing.T) {
	operation := podDeleteOperation(t)
	operation.TargetType = "workload"
	queries := &podDeleteQueriesFake{operation: operation}
	k8s := &podDeleteK8sFake{status: http.StatusNoContent}
	ctx := testRuntimeContext(RuntimeDependencies{Queries: queries, K8s: k8s})
	task, _ := NewPodDeleteTask(operation.ID)
	err := HandlePodDelete(ctx, task)
	if !errors.Is(err, asynq.SkipRetry) || queries.operation.Status != "failed" || queries.operation.ErrorMessage != "pod delete failed: invalid_target" {
		t.Fatalf("error=%v operation=%+v", err, queries.operation)
	}
	if len(k8s.calls) != 0 || len(queries.events) != 1 || strings.Contains(string(queries.events[0].Detail), operation.TargetKey) {
		t.Fatalf("calls=%+v evidence=%+v", k8s.calls, queries.events)
	}
}

func TestNewPodDeleteTaskRejectsMissingOperationID(t *testing.T) {
	if _, err := NewPodDeleteTask(uuid.Nil); err == nil {
		t.Fatal("expected missing operation ID error")
	}
}

func TestClassifyPodDeleteHTTPStatusTerminality(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity} {
		if _, terminal := classifyPodDeleteError(nil, status); !terminal {
			t.Fatalf("status %d must be terminal", status)
		}
	}
	for _, status := range []int{http.StatusConflict, http.StatusTooManyRequests, http.StatusInternalServerError} {
		if _, terminal := classifyPodDeleteError(nil, status); terminal {
			t.Fatalf("status %d must remain retryable", status)
		}
	}
}

func TestHandlePodDeleteStaleAttemptCannotPersistOutcome(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		err    error
	}{
		{name: "completed", status: http.StatusNoContent},
		{name: "retrying", err: errors.New("tunnel timeout")},
		{name: "failed", status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operation := podDeleteOperation(t)
			queries := &podDeleteQueriesFake{operation: operation}
			k8s := &podDeleteK8sFake{status: tc.status, err: tc.err, onDo: func() {
				// Simulate the lease expiring and a newer worker claiming attempt 2
				// while attempt 1 is still returning from Kubernetes.
				queries.operation.AttemptCount++
				queries.operation.Status = "running"
			}}
			ctx := testRuntimeContext(RuntimeDependencies{Queries: queries, K8s: k8s})
			task, _ := NewPodDeleteTask(operation.ID)
			if err := HandlePodDelete(ctx, task); err != nil {
				t.Fatalf("stale attempt must exit as a no-op: %v", err)
			}
			if queries.operation.Status != "running" || queries.operation.AttemptCount != 2 || len(queries.events) != 0 {
				t.Fatalf("newer attempt was overwritten: operation=%+v events=%+v", queries.operation, queries.events)
			}
		})
	}
}
