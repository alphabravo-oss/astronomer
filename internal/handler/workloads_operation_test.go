package handler

import (
	"bytes"
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

type workloadMutationQuerier struct {
	WorkloadQuerier
	operations []sqlc.WorkloadOperation
	audits     []sqlc.CreateAuditLogV1Params
}

func (q *workloadMutationQuerier) CreateWorkloadOperation(_ context.Context, arg sqlc.CreateWorkloadOperationParams) (sqlc.WorkloadOperation, error) {
	op := sqlc.WorkloadOperation{
		ID:            uuid.New(),
		TargetType:    arg.TargetType,
		TargetKey:     arg.TargetKey,
		OperationType: arg.OperationType,
		Payload:       arg.Payload,
		Status:        arg.Status,
		CreatedByID:   arg.CreatedByID,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	q.operations = append(q.operations, op)
	return op, nil
}

func (q *workloadMutationQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	q.audits = append(q.audits, arg)
	return nil
}

func TestWorkloadMutationsAreAudited(t *testing.T) {
	q := &workloadMutationQuerier{}
	h := NewWorkloadHandlerWithDeps(q, nil)
	clusterID := uuid.NewString()

	scaleReq := workloadRouteRequest(http.MethodPatch, "/scale/", map[string]string{
		"cluster_id": clusterID,
		"kind":       "deployment",
		"namespace":  "default",
		"name":       "api",
	}, map[string]any{"replicas": 3})
	scaleRec := httptest.NewRecorder()
	h.Scale(scaleRec, scaleReq)
	if scaleRec.Code != http.StatusAccepted {
		t.Fatalf("scale status=%d body=%s", scaleRec.Code, scaleRec.Body.String())
	}
	assertWorkloadAudit(t, q.audits[0], "workload.scale", "deployment/default/api")
	assertAuditDetail(t, q.audits[0].Detail, "clusterId", clusterID)

	restartReq := workloadRouteRequest(http.MethodPost, "/restart/", map[string]string{
		"cluster_id": clusterID,
		"kind":       "deployment",
		"namespace":  "default",
		"name":       "api",
	}, nil)
	restartRec := httptest.NewRecorder()
	h.Restart(restartRec, restartReq)
	if restartRec.Code != http.StatusAccepted {
		t.Fatalf("restart status=%d body=%s", restartRec.Code, restartRec.Body.String())
	}
	assertWorkloadAudit(t, q.audits[1], "workload.restart", "deployment/default/api")
	assertAuditDetail(t, q.audits[1].Detail, "clusterId", clusterID)

	deleteReq := workloadRouteRequest(http.MethodDelete, "/delete/", map[string]string{
		"cluster_id": clusterID,
		"kind":       "deployment",
		"namespace":  "default",
		"name":       "api",
	}, nil)
	deleteRec := httptest.NewRecorder()
	h.Delete(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusAccepted {
		t.Fatalf("delete status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	assertWorkloadAudit(t, q.audits[2], "workload.delete", "deployment/default/api")
	assertAuditDetail(t, q.audits[2].Detail, "clusterId", clusterID)
}

func workloadRouteRequest(method, target string, params map[string]string, body any) *http.Request {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(raw))
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func assertWorkloadAudit(t *testing.T, row sqlc.CreateAuditLogV1Params, action, resourceID string) {
	t.Helper()
	if row.Action != action || row.ResourceType != "workload" || row.ResourceID != resourceID || row.ResourceName != "api" {
		t.Fatalf("audit row=%+v, want %s on workload %s", row, action, resourceID)
	}
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
