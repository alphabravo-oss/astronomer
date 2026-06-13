package handler

import (
	"context"
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

type stubMonitoringRBACQuerier struct {
	bindings []rbac.RoleBinding
	err      error
}

func (s stubMonitoringRBACQuerier) GetUserBindings(context.Context, string) ([]rbac.RoleBinding, error) {
	return s.bindings, s.err
}

type monitoringRetryQuerier struct {
	MonitoringQuerier
	operations map[uuid.UUID]sqlc.MonitoringOperation
	requeued   bool
}

func (q *monitoringRetryQuerier) GetMonitoringOperation(_ context.Context, id uuid.UUID) (sqlc.MonitoringOperation, error) {
	op, ok := q.operations[id]
	if !ok {
		return sqlc.MonitoringOperation{}, errors.New("not found")
	}
	return op, nil
}

func (q *monitoringRetryQuerier) RequeueMonitoringOperation(_ context.Context, id uuid.UUID) (sqlc.MonitoringOperation, error) {
	op, ok := q.operations[id]
	if !ok {
		return sqlc.MonitoringOperation{}, errors.New("not found")
	}
	q.requeued = true
	op.Status = OpStatusPending
	op.UpdatedAt = time.Now()
	q.operations[id] = op
	return op, nil
}

func TestRetryMonitoringOperationDeniedWithoutClusterUpdate(t *testing.T) {
	clusterID := uuid.New()
	op := sqlc.MonitoringOperation{
		ID:            uuid.New(),
		TargetType:    "cluster_stack",
		TargetKey:     clusterID.String(),
		OperationType: "apply",
		Status:        OpStatusFailed,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	q := &monitoringRetryQuerier{operations: map[uuid.UUID]sqlc.MonitoringOperation{op.ID: op}}
	h := NewMonitoringHandlerWithQueries(q, nil)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{
		bindings: []rbac.RoleBinding{{
			ClusterID: clusterID.String(),
			RoleRules: []rbac.Rule{{
				Resource: string(rbac.ResourceMonitoring),
				Verbs:    []string{string(rbac.VerbRead)},
			}},
		}},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitoring/operations/"+op.ID.String()+"/retry/", nil)
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
		t.Fatalf("operation was requeued without monitoring:update permission")
	}
	if got := q.operations[op.ID].Status; got != OpStatusFailed {
		t.Fatalf("status = %q, want %q", got, OpStatusFailed)
	}
}
