package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type stubWorkloadRBACQuerier struct {
	bindings []rbac.RoleBinding
	err      error
}

func (s stubWorkloadRBACQuerier) GetUserBindings(context.Context, string) ([]rbac.RoleBinding, error) {
	return s.bindings, s.err
}

type workloadRetryQuerier struct {
	WorkloadQuerier
	operations map[uuid.UUID]sqlc.WorkloadOperation
	requeued   bool
}

func (q *workloadRetryQuerier) GetWorkloadOperation(_ context.Context, id uuid.UUID) (sqlc.WorkloadOperation, error) {
	op, ok := q.operations[id]
	if !ok {
		return sqlc.WorkloadOperation{}, errors.New("not found")
	}
	return op, nil
}

func (q *workloadRetryQuerier) RequeueWorkloadOperation(_ context.Context, id uuid.UUID) (sqlc.WorkloadOperation, error) {
	op, ok := q.operations[id]
	if !ok {
		return sqlc.WorkloadOperation{}, errors.New("not found")
	}
	q.requeued = true
	op.Status = OpStatusPending
	op.UpdatedAt = time.Now()
	q.operations[id] = op
	return op, nil
}

func TestRetryWorkloadOperationDeniedWithoutClusterUpdate(t *testing.T) {
	clusterID := uuid.New()
	payload, err := json.Marshal(workloadOperationEnvelope{
		ClusterID: clusterID.String(),
		Kind:      "Deployment",
		Namespace: "default",
		Name:      "api",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	op := sqlc.WorkloadOperation{
		ID:            uuid.New(),
		TargetType:    "workload",
		TargetKey:     clusterID.String() + ":default:Deployment:api",
		OperationType: "scale",
		Payload:       payload,
		Status:        OpStatusFailed,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	q := &workloadRetryQuerier{operations: map[uuid.UUID]sqlc.WorkloadOperation{op.ID: op}}
	h := NewWorkloadHandlerWithDeps(q, nil)
	h.SetAuthorization(rbac.NewEngine(), stubWorkloadRBACQuerier{
		bindings: []rbac.RoleBinding{{
			ClusterID: clusterID.String(),
			RoleRules: []rbac.Rule{{
				Resource: string(rbac.ResourceWorkloads),
				Verbs:    []string{string(rbac.VerbRead)},
			}},
		}},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workloads/operations/"+op.ID.String()+"/retry/", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", op.ID.String())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = middleware.SetAuthenticatedUserForTest(ctx, &middleware.AuthenticatedUser{ID: uuid.NewString()})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.RetryOperation(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	if q.requeued {
		t.Fatalf("operation was requeued without workloads:update permission")
	}
	if got := q.operations[op.ID].Status; got != OpStatusFailed {
		t.Fatalf("status = %q, want %q", got, OpStatusFailed)
	}
}
