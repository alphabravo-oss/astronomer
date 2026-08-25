package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type stagedToolMutationTx struct {
	ToolMutationTx
	ops      []sqlc.ToolOperation
	audits   []sqlc.UpsertAuditOutboxParams
	auditErr error
	idemOp   sqlc.ToolOperation
}

func (tx *stagedToolMutationTx) CreateToolOperation(_ context.Context, arg sqlc.CreateToolOperationParams) (sqlc.ToolOperation, error) {
	op := sqlc.ToolOperation{ID: uuid.New(), TargetType: arg.TargetType, TargetKey: arg.TargetKey, OperationType: arg.OperationType, Payload: arg.Payload, Status: arg.Status}
	tx.ops = append(tx.ops, op)
	return op, nil
}

func (tx *stagedToolMutationTx) CreateToolOperationIdempotent(ctx context.Context, arg sqlc.CreateToolOperationIdempotentParams) (sqlc.ToolOperation, error) {
	if tx.idemOp.ID != uuid.Nil {
		return tx.idemOp, nil
	}
	return tx.CreateToolOperation(ctx, sqlc.CreateToolOperationParams{
		TargetType: arg.TargetType, TargetKey: arg.TargetKey, OperationType: arg.OperationType,
		Payload: arg.Payload, Status: arg.Status, CreatedByID: arg.CreatedByID,
	})
}

func (tx *stagedToolMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestToolOperationAndAuditCommitTogether(t *testing.T) {
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
			h := NewToolHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(ToolMutationTx) error) error {
				tx := &stagedToolMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedOperations += len(tx.ops)
				committedAudits += len(tx.audits)
				return nil
			})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tools/loki/install/", nil)

			_, err := executeToolMutation(request, h,
				func(q ToolMutationTx) (sqlc.ToolOperation, error) {
					return createToolOperation(request.Context(), q, "tool_installation", "cluster/loki", "install", toolOperationEnvelope{ClusterID: uuid.NewString(), ToolSlug: "loki"}, sqlc.CreateToolOperationParams{}.CreatedByID)
				},
				func() (sqlc.ToolOperation, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.ToolOperation{}, nil
				},
				func(op sqlc.ToolOperation) clusterAuditEvent {
					return clusterAuditEvent{action: "tool.install", resourceType: "tool", resourceID: "loki", status: http.StatusAccepted, detail: map[string]any{"operation_id": op.ID.String()}}
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

func TestToolIdempotencyKeyCannotAliasDifferentOperation(t *testing.T) {
	tx := &stagedToolMutationTx{idemOp: sqlc.ToolOperation{
		ID: uuid.New(), TargetType: "tool_installation", TargetKey: "cluster/loki",
		OperationType: "install", Payload: []byte(`{"cluster_id":"prior"}`), Status: "pending",
	}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tools/prometheus/install/", nil)
	request.Header.Set("Idempotency-Key", "one-logical-operation")

	_, err := createToolOperation(withOperationIdempotency(request, "tools"), tx,
		"tool_installation", "cluster/prometheus", "install",
		toolOperationEnvelope{ClusterID: uuid.NewString(), ToolSlug: "prometheus"}, sqlc.CreateToolOperationParams{}.CreatedByID)
	if !errors.Is(err, errToolOperationIdempotencyConflict) {
		t.Fatalf("error = %v, want idempotency conflict", err)
	}
}
