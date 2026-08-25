package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type stagedWorkloadMutationTx struct {
	WorkloadMutationTx
	ops       []sqlc.WorkloadOperation
	tasks     []sqlc.UpsertTaskOutboxParams
	audits    []sqlc.UpsertAuditOutboxParams
	taskErr   error
	auditErr  error
	idemOp    sqlc.WorkloadOperation
	idemCalls int
	reserved  sqlc.OperationIdempotencyKey
}

func (tx *stagedWorkloadMutationTx) CreateWorkloadOperation(_ context.Context, arg sqlc.CreateWorkloadOperationParams) (sqlc.WorkloadOperation, error) {
	op := sqlc.WorkloadOperation{ID: uuid.New(), TargetType: arg.TargetType, TargetKey: arg.TargetKey, OperationType: arg.OperationType, Payload: arg.Payload, Status: arg.Status}
	tx.ops = append(tx.ops, op)
	return op, nil
}

func (tx *stagedWorkloadMutationTx) CreateWorkloadOperationIdempotent(ctx context.Context, arg sqlc.CreateWorkloadOperationIdempotentParams) (sqlc.WorkloadOperation, error) {
	tx.idemCalls++
	if tx.idemOp.ID != uuid.Nil {
		return tx.idemOp, nil
	}
	return tx.CreateWorkloadOperation(ctx, sqlc.CreateWorkloadOperationParams{
		TargetType: arg.TargetType, TargetKey: arg.TargetKey, OperationType: arg.OperationType,
		Payload: arg.Payload, Status: arg.Status, CreatedByID: arg.CreatedByID,
	})
}

func (tx *stagedWorkloadMutationTx) ReserveOperationIdempotencyKey(_ context.Context, arg sqlc.ReserveOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error) {
	tx.idemCalls++
	if tx.reserved.Scope == "" {
		tx.reserved.Scope = arg.Scope
		tx.reserved.IdempotencyKey = arg.IdempotencyKey
		if tx.idemOp.ID != uuid.Nil {
			tx.reserved.OperationTable = "workload_operations"
			tx.reserved.OperationID = pgtype.UUID{Bytes: tx.idemOp.ID, Valid: true}
		}
	}
	return tx.reserved, nil
}

func (tx *stagedWorkloadMutationTx) AttachOperationIdempotencyKey(_ context.Context, arg sqlc.AttachOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error) {
	tx.reserved.Scope = arg.Scope
	tx.reserved.IdempotencyKey = arg.IdempotencyKey
	tx.reserved.OperationTable = arg.OperationTable
	tx.reserved.OperationID = pgtype.UUID{Bytes: arg.OperationID, Valid: true}
	tx.reserved.Response = arg.Response
	return tx.reserved, nil
}

func (tx *stagedWorkloadMutationTx) GetWorkloadOperation(_ context.Context, id uuid.UUID) (sqlc.WorkloadOperation, error) {
	for _, op := range tx.ops {
		if op.ID == id {
			return op, nil
		}
	}
	if tx.idemOp.ID == id {
		return tx.idemOp, nil
	}
	return sqlc.WorkloadOperation{}, errors.New("operation not found")
}

func (tx *stagedWorkloadMutationTx) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	if tx.taskErr != nil {
		return sqlc.TaskOutbox{}, tx.taskErr
	}
	tx.tasks = append(tx.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType, Payload: arg.Payload}, nil
}

func (tx *stagedWorkloadMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestWorkloadOperationAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit both", wantCommit: 1},
		{name: "audit failure rolls back operation", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedOperations, committedAudits := 0, 0
			h := NewWorkloadHandler()
			h.SetRunTx(func(_ context.Context, fn func(WorkloadMutationTx) error) error {
				tx := &stagedWorkloadMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedOperations += len(tx.ops)
				committedAudits += len(tx.audits)
				return nil
			})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/c/workloads/deployment/apps/web/restart/", nil)

			_, err := executeWorkloadMutation(request, h,
				func(q WorkloadMutationTx) (sqlc.WorkloadOperation, error) {
					return createWorkloadOperation(request.Context(), q, "workload", "c:deployment:apps:web", "restart", workloadOperationEnvelope{ClusterID: uuid.NewString(), Kind: "deployment", Namespace: "apps", Name: "web"}, sqlc.CreateWorkloadOperationParams{}.CreatedByID)
				},
				func() (sqlc.WorkloadOperation, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.WorkloadOperation{}, nil
				},
				func(op sqlc.WorkloadOperation) clusterAuditEvent {
					return clusterAuditEvent{action: "workload.restart", resourceType: "workload", resourceID: "deployment/apps/web", status: http.StatusAccepted, detail: map[string]any{"operation_id": op.ID.String()}}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedOperations != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed operation/audit = %d/%d, want %d each", committedOperations, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestWorkloadIdempotencyKeyCannotAliasDifferentOperation(t *testing.T) {
	tx := &stagedWorkloadMutationTx{idemOp: sqlc.WorkloadOperation{
		ID: uuid.New(), TargetType: "workload", TargetKey: "c:deployment:apps:web",
		OperationType: "restart", Payload: []byte(`{"clusterId":"prior"}`), Status: "pending",
	}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/c/workloads/deployment/apps/api/restart/", nil)
	request.Header.Set("Idempotency-Key", "one-logical-operation")

	_, err := createWorkloadOperation(withOperationIdempotency(request, "workloads"), tx,
		"workload", "c:deployment:apps:api", "restart",
		workloadOperationEnvelope{ClusterID: uuid.NewString(), Kind: "deployment", Namespace: "apps", Name: "api"}, sqlc.CreateWorkloadOperationParams{}.CreatedByID)
	if !errors.Is(err, errWorkloadOperationIdempotencyConflict) {
		t.Fatalf("error = %v, want idempotency conflict", err)
	}
}
