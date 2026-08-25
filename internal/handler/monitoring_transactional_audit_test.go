package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type stagedMonitoringMutationTx struct {
	MonitoringMutationTx
	fakeOperationIdempotencyStore
	backend  sqlc.MonitoringBackend
	ops      []sqlc.MonitoringOperation
	audits   []sqlc.UpsertAuditOutboxParams
	auditErr error
	idemOp   sqlc.MonitoringOperation
	retryOp  sqlc.MonitoringOperation
	requeues int
}

func (tx *stagedMonitoringMutationTx) RequeueMonitoringOperation(_ context.Context, id uuid.UUID) (sqlc.MonitoringOperation, error) {
	tx.requeues++
	row := tx.retryOp
	row.ID, row.Status = id, OpStatusPending
	return row, nil
}

func (tx *stagedMonitoringMutationTx) UpsertDefaultMonitoringBackend(_ context.Context, arg sqlc.UpsertDefaultMonitoringBackendParams) (sqlc.MonitoringBackend, error) {
	tx.backend = sqlc.MonitoringBackend{ID: uuid.New(), BackendType: arg.BackendType, QueryUrl: arg.QueryUrl, TenantID: arg.TenantID, AuthType: arg.AuthType}
	return tx.backend, nil
}

func (tx *stagedMonitoringMutationTx) GetDefaultMonitoringBackend(context.Context) (sqlc.MonitoringBackend, error) {
	return tx.backend, nil
}

func (tx *stagedMonitoringMutationTx) CreateMonitoringOperation(_ context.Context, arg sqlc.CreateMonitoringOperationParams) (sqlc.MonitoringOperation, error) {
	op := sqlc.MonitoringOperation{ID: uuid.New(), TargetType: arg.TargetType, TargetKey: arg.TargetKey, OperationType: arg.OperationType, Payload: arg.Payload, Status: arg.Status}
	tx.ops = append(tx.ops, op)
	return op, nil
}

func (tx *stagedMonitoringMutationTx) CreateMonitoringOperationIdempotent(ctx context.Context, arg sqlc.CreateMonitoringOperationIdempotentParams) (sqlc.MonitoringOperation, error) {
	if tx.idemOp.ID != uuid.Nil {
		return tx.idemOp, nil
	}
	return tx.CreateMonitoringOperation(ctx, sqlc.CreateMonitoringOperationParams{
		TargetType: arg.TargetType, TargetKey: arg.TargetKey, OperationType: arg.OperationType,
		Payload: arg.Payload, Status: arg.Status, CreatedByID: arg.CreatedByID,
	})
}

func (tx *stagedMonitoringMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestMonitoringBackendAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit both", wantCommit: 1},
		{name: "audit failure rolls back backend", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedBackends, committedAudits := 0, 0
			h := NewMonitoringHandler()
			h.SetRunTx(func(_ context.Context, fn func(MonitoringMutationTx) error) error {
				tx := &stagedMonitoringMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				if tx.backend.ID != uuid.Nil {
					committedBackends++
				}
				committedAudits += len(tx.audits)
				return nil
			})
			request := httptest.NewRequest(http.MethodPut, "/api/v1/settings/monitoring/", nil)
			params := sqlc.UpsertDefaultMonitoringBackendParams{BackendType: "thanos", QueryUrl: "https://thanos.example.com"}

			_, err := executeMonitoringMutation(request, h,
				func(q MonitoringMutationTx) (sqlc.MonitoringBackend, error) {
					return q.UpsertDefaultMonitoringBackend(request.Context(), params)
				},
				func() (sqlc.MonitoringBackend, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.MonitoringBackend{}, nil
				},
				func(row sqlc.MonitoringBackend) clusterAuditEvent {
					return clusterAuditEvent{action: "monitoring.endpoint.update", resourceType: "monitoring_backend", resourceID: row.ID.String(), status: http.StatusOK}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedBackends != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed backend/audit = %d/%d, want %d each", committedBackends, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestMonitoringIdempotencyKeyCannotAliasDifferentOperation(t *testing.T) {
	tx := &stagedMonitoringMutationTx{idemOp: sqlc.MonitoringOperation{
		ID: uuid.New(), TargetType: "cluster_stack", TargetKey: "cluster-a",
		OperationType: "install", Payload: []byte(`{"cluster_id":"cluster-a"}`), Status: "pending",
	}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/cluster-b/monitoring/install/", nil)
	request.Header.Set("Idempotency-Key", "one-logical-operation")
	params := sqlc.CreateMonitoringOperationParams{TargetType: "cluster_stack", TargetKey: "cluster-b", OperationType: "install", Payload: []byte(`{"cluster_id":"cluster-b"}`), Status: "pending"}

	_, err := createMonitoringOperationWith(withOperationIdempotency(request, "monitoring"), tx, params)
	if !errors.Is(err, errMonitoringOperationIdempotencyConflict) {
		t.Fatalf("error = %v, want idempotency conflict", err)
	}
}

func TestMonitoringRetryReplaysExactReceiptOnceUnderRace(t *testing.T) {
	callerID, clusterID := uuid.New(), uuid.New()
	op := sqlc.MonitoringOperation{
		ID: uuid.New(), TargetType: "cluster_stack", TargetKey: clusterID.String(),
		OperationType: "apply", Status: OpStatusFailed,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	queries := &monitoringRetryQuerier{operations: map[uuid.UUID]sqlc.MonitoringOperation{op.ID: op}}
	tx := &stagedMonitoringMutationTx{retryOp: op}
	h := NewMonitoringHandlerWithQueries(queries, nil)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: []rbac.RoleBinding{{
		ClusterID: clusterID.String(), RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceMonitoring), Verbs: []string{string(rbac.VerbUpdate)}}},
	}}})
	var transactionMu sync.Mutex
	h.SetRunTx(func(_ context.Context, fn func(MonitoringMutationTx) error) error {
		transactionMu.Lock()
		defer transactionMu.Unlock()
		return fn(tx)
	})
	router := chi.NewRouter()
	router.Post("/api/v1/settings/monitoring/operations/{id}/retry/", h.RetryOperation)
	responses := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/monitoring/operations/"+op.ID.String()+"/retry/", nil)
			req.Header.Set("Idempotency-Key", "monitoring-retry-race")
			req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: callerID.String()}))
			responses[index] = httptest.NewRecorder()
			router.ServeHTTP(responses[index], req)
		}(i)
	}
	wg.Wait()
	for _, response := range responses {
		if response.Code != http.StatusAccepted {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if responses[0].Body.String() != responses[1].Body.String() || responses[0].Header().Get("Location") != responses[1].Header().Get("Location") {
		t.Fatalf("replay receipt changed: first=%s second=%s", responses[0].Body.String(), responses[1].Body.String())
	}
	if tx.requeues != 1 || len(tx.audits) != 1 {
		t.Fatalf("racing replay requeues/audits=%d/%d, want 1/1", tx.requeues, len(tx.audits))
	}
}

func TestMonitoringRetryKeyRejectsChangedTarget(t *testing.T) {
	callerID, clusterID := uuid.New(), uuid.New()
	firstOp := sqlc.MonitoringOperation{ID: uuid.New(), TargetType: "cluster_stack", TargetKey: clusterID.String(), OperationType: "apply", Status: OpStatusFailed, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	secondOp := firstOp
	secondOp.ID = uuid.New()
	queries := &monitoringRetryQuerier{operations: map[uuid.UUID]sqlc.MonitoringOperation{firstOp.ID: firstOp, secondOp.ID: secondOp}}
	tx := &stagedMonitoringMutationTx{retryOp: firstOp}
	h := NewMonitoringHandlerWithQueries(queries, nil)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: []rbac.RoleBinding{{
		ClusterID: clusterID.String(), RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceMonitoring), Verbs: []string{string(rbac.VerbUpdate)}}},
	}}})
	h.SetRunTx(func(_ context.Context, fn func(MonitoringMutationTx) error) error { return fn(tx) })
	router := chi.NewRouter()
	router.Post("/api/v1/settings/monitoring/operations/{id}/retry/", h.RetryOperation)
	request := func(id uuid.UUID) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/monitoring/operations/"+id.String()+"/retry/", nil)
		req.Header.Set("Idempotency-Key", "monitoring-retry-conflict")
		req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: callerID.String()}))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	first, changed := request(firstOp.ID), request(secondOp.ID)
	if first.Code != http.StatusAccepted || changed.Code != http.StatusConflict {
		t.Fatalf("first/changed status=%d/%d changed=%s", first.Code, changed.Code, changed.Body.String())
	}
	if tx.requeues != 1 || len(tx.audits) != 1 {
		t.Fatalf("changed target requeues/audits=%d/%d", tx.requeues, len(tx.audits))
	}
}

func TestSharedMonitoringMetadataOperationAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit all", wantCommit: 1},
		{name: "audit failure rolls back all", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedBackends, committedOps, committedAudits, afterCommitCalls := 0, 0, 0, 0
			h := NewMonitoringHandler()
			h.SetRunTx(func(_ context.Context, fn func(MonitoringMutationTx) error) error {
				tx := &stagedMonitoringMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				if tx.backend.ID != uuid.Nil {
					committedBackends++
				}
				committedOps += len(tx.ops)
				committedAudits += len(tx.audits)
				return nil
			})
			lifecycle := sharedStackLifecycle[string]{
				h:           h,
				auditPrefix: "monitoring.shared_test",
				noun:        "test",
				persistWith: func(ctx context.Context, q monitoringSharedMutationWriter, _ sqlc.MonitoringBackend, _ string, _ string) error {
					_, err := q.UpsertDefaultMonitoringBackend(ctx, sqlc.UpsertDefaultMonitoringBackendParams{BackendType: "thanos"})
					return err
				},
				enqueueWith: func(ctx context.Context, q monitoringSharedMutationWriter, userID pgtype.UUID, operationType, _ string, _ map[string]any, _ *objectStoreSecretSpec) (sqlc.MonitoringOperation, error) {
					return q.CreateMonitoringOperation(ctx, sqlc.CreateMonitoringOperationParams{
						TargetType: "shared_test", TargetKey: "shared", OperationType: operationType,
						Payload: []byte(`{"desired":"state"}`), Status: OpStatusPending, CreatedByID: userID,
					})
				},
				afterCommit: func(context.Context, string) { afterCommitCalls++ },
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/settings/monitoring/test/install/", nil)
			backend := sqlc.MonitoringBackend{ID: uuid.New(), BackendType: "thanos"}

			_, err := lifecycle.stageMutation(request, backend, "desired", "desired", "installing", "install", map[string]any{"replicas": 2}, nil, "cluster-a", "monitoring", "test")
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedBackends != tc.wantCommit || committedOps != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed backend/operation/audit = %d/%d/%d, want %d each", committedBackends, committedOps, committedAudits, tc.wantCommit)
			}
			if afterCommitCalls != tc.wantCommit {
				t.Fatalf("afterCommit calls = %d, want %d", afterCommitCalls, tc.wantCommit)
			}
		})
	}
}

func TestMonitoringIdempotencyConflictReturnsHTTP409(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/settings/monitoring/test/install/", nil)
	response := httptest.NewRecorder()
	respondMonitoringMutationError(response, request, errMonitoringOperationIdempotencyConflict,
		http.StatusInternalServerError, "monitoring_error", "failed")
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
}
