package handler

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type stagedLoggingMutationTx struct {
	*loggingFakeQuerier
	idemOp   sqlc.LoggingOperation
	audits   []sqlc.UpsertAuditOutboxParams
	auditErr error
}

func newStagedLoggingMutationTx() *stagedLoggingMutationTx {
	return &stagedLoggingMutationTx{loggingFakeQuerier: newLoggingFakeQuerier()}
}

func (tx *stagedLoggingMutationTx) CreateLoggingOperationIdempotent(ctx context.Context, arg sqlc.CreateLoggingOperationIdempotentParams) (sqlc.LoggingOperation, error) {
	if tx.idemOp.ID != uuid.Nil {
		return tx.idemOp, nil
	}
	return tx.CreateLoggingOperation(ctx, sqlc.CreateLoggingOperationParams{
		TargetType: arg.TargetType, TargetKey: arg.TargetKey, OperationType: arg.OperationType,
		Payload: arg.Payload, Status: arg.Status, CreatedByID: arg.CreatedByID,
	})
}

func (tx *stagedLoggingMutationTx) CreateLoggingOperationIdempotentWithDisposition(ctx context.Context, arg sqlc.CreateLoggingOperationIdempotentWithDispositionParams) (sqlc.CreateLoggingOperationIdempotentWithDispositionRow, error) {
	if tx.idemOp.ID != uuid.Nil {
		return sqlc.CreateLoggingOperationIdempotentWithDispositionRow{LoggingOperation: tx.idemOp, Inserted: false}, nil
	}
	op, err := tx.CreateLoggingOperation(ctx, sqlc.CreateLoggingOperationParams{
		TargetType: arg.TargetType, TargetKey: arg.TargetKey, OperationType: arg.OperationType,
		Payload: arg.Payload, Status: arg.Status, CreatedByID: arg.CreatedByID,
	})
	return sqlc.CreateLoggingOperationIdempotentWithDispositionRow{LoggingOperation: op, Inserted: err == nil}, err
}

func (tx *stagedLoggingMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestLoggingStateOperationAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit all", wantCommit: 1},
		{name: "audit failure rolls back state and operation", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedState, committedOperations, committedAudits := 0, 0, 0
			h := NewLoggingHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(LoggingMutationTx) error) error {
				tx := newStagedLoggingMutationTx()
				tx.auditErr = tc.auditErr
				if err := fn(tx); err != nil {
					return err
				}
				committedState += len(tx.outputs)
				committedOperations += len(tx.operations)
				committedAudits += len(tx.audits)
				return nil
			})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/logging/outputs/", nil)
			clusterID := uuid.New()
			params := sqlc.CreateLoggingOutputParams{Name: "loki", OutputType: "loki", Configuration: []byte(`{}`), ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true}}

			_, err := executeLoggingMutation(request, h,
				func(q LoggingMutationTx) (loggingMutationResult[sqlc.LoggingOutput], error) {
					row, mutationErr := q.CreateLoggingOutput(request.Context(), params)
					if mutationErr != nil {
						return loggingMutationResult[sqlc.LoggingOutput]{}, mutationErr
					}
					op, mutationErr := createLoggingOutputApplyOperation(request.Context(), q, row, pgtype.UUID{})
					return loggingMutationResult[sqlc.LoggingOutput]{row: row, op: op}, mutationErr
				},
				func() (loggingMutationResult[sqlc.LoggingOutput], error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return loggingMutationResult[sqlc.LoggingOutput]{}, nil
				},
				func(result loggingMutationResult[sqlc.LoggingOutput]) clusterAuditEvent {
					return clusterAuditEvent{action: "logging.output.create", resourceType: "logging_output", resourceID: result.row.ID.String(), status: http.StatusCreated, detail: map[string]any{"operation_id": result.op.ID.String()}}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedState != tc.wantCommit || committedOperations != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed state/operation/audit = %d/%d/%d, want %d each", committedState, committedOperations, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestLoggingSavedSearchAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantCommit int
	}{
		{name: "commit saved search and audit", wantCommit: 1},
		{name: "audit failure rolls back saved search", auditErr: errors.New("audit unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedSearches, committedAudits := 0, 0
			h := NewLoggingHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(LoggingMutationTx) error) error {
				tx := newStagedLoggingMutationTx()
				tx.auditErr = tc.auditErr
				if err := fn(tx); err != nil {
					return err
				}
				committedSearches += len(tx.saved)
				committedAudits += len(tx.audits)
				return nil
			})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/logging/saved-searches/", nil)
			params := sqlc.CreateLoggingSavedSearchParams{
				OutputID: uuid.New(), OwnerUserID: uuid.New(), Name: "errors", QueryText: `{app="api"}`,
				ResultLimit: 100, Direction: "backward",
			}

			_, err := executeLoggingMutation(request, h,
				func(q LoggingMutationTx) (sqlc.LoggingSavedSearch, error) {
					return q.CreateLoggingSavedSearch(request.Context(), params)
				},
				func() (sqlc.LoggingSavedSearch, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.LoggingSavedSearch{}, nil
				},
				loggingSavedSearchAuditEvent("logging.saved_search.create", http.StatusCreated),
			)
			if (err != nil) != (tc.auditErr != nil) {
				t.Fatalf("error=%v auditErr=%v", err, tc.auditErr)
			}
			if committedSearches != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed search/audit=%d/%d want=%d", committedSearches, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestLoggingReplayCannotCommitOrphanConfiguration(t *testing.T) {
	h := NewLoggingHandler(nil)
	commits := 0
	h.SetRunTx(func(_ context.Context, fn func(LoggingMutationTx) error) error {
		tx := newStagedLoggingMutationTx()
		tx.idemOp = sqlc.LoggingOperation{ID: uuid.New(), TargetType: "output", TargetKey: uuid.NewString(), OperationType: "apply", Payload: []byte(`{"cluster_id":"prior"}`), Status: "pending"}
		if err := fn(tx); err != nil {
			return err
		}
		commits++
		return nil
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/logging/outputs/", nil)
	request.Header.Set("Idempotency-Key", "create-loki-once")
	clusterID := uuid.New()
	params := sqlc.CreateLoggingOutputParams{Name: "loki", OutputType: "loki", Configuration: []byte(`{}`), ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true}}
	opCtx := withOperationIdempotency(request, "logging")

	_, err := executeLoggingMutation(request, h,
		func(q LoggingMutationTx) (loggingMutationResult[sqlc.LoggingOutput], error) {
			row, mutationErr := q.CreateLoggingOutput(request.Context(), params)
			if mutationErr != nil {
				return loggingMutationResult[sqlc.LoggingOutput]{}, mutationErr
			}
			op, mutationErr := createLoggingOutputApplyOperation(opCtx, q, row, pgtype.UUID{})
			return loggingMutationResult[sqlc.LoggingOutput]{row: row, op: op}, mutationErr
		},
		func() (loggingMutationResult[sqlc.LoggingOutput], error) {
			return loggingMutationResult[sqlc.LoggingOutput]{}, nil
		},
		func(result loggingMutationResult[sqlc.LoggingOutput]) clusterAuditEvent {
			return clusterAuditEvent{action: "logging.output.create", resourceType: "logging_output", resourceID: result.row.ID.String(), status: http.StatusCreated}
		})
	if !errors.Is(err, errLoggingOperationIdempotencyConflict) {
		t.Fatalf("error = %v, want idempotency conflict", err)
	}
	if commits != 0 {
		t.Fatal("a replay with an existing operation committed orphan logging configuration")
	}
}

func TestLoggingHighRiskMutationsUseTransactionalExecutor(t *testing.T) {
	want := map[string]bool{
		"CreateOutput": false, "UpdateOutput": false, "TestOutput": false, "DeleteOutput": false,
		"CreatePipeline": false, "UpdatePipeline": false, "DeletePipeline": false,
		"setOutputEnabled": false, "setPipelineEnabled": false, "RetryOperation": false,
		"RotateOutputToken": false, "AttachAstronomerLogs": false,
		"CreateSavedSearch": false, "UpdateSavedSearch": false, "DeleteSavedSearch": false,
	}
	for _, name := range []string{"logging.go", "logging_loki_token.go", "logging_attach.go", "logging_saved_searches.go"} {
		path, err := filepath.Abs(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if _, tracked := want[fn.Name.Name]; !tracked {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeLoggingMutation" {
					want[fn.Name.Name] = true
				}
				return true
			})
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeLoggingMutation", name)
		}
	}
}
