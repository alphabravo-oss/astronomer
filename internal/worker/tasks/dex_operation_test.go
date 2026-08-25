package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type dexTaskStore struct {
	row                         sqlc.DexOperation
	succeeded, retrying, failed int
}

func (s *dexTaskStore) RecoverDexOperationOutbox(context.Context, time.Time) (int64, error) {
	return 1, nil
}

func (s *dexTaskStore) ClaimDexOperation(context.Context, sqlc.ClaimDexOperationParams) (sqlc.DexOperation, error) {
	if s.row.Status == "succeeded" || s.row.Status == "failed" {
		return sqlc.DexOperation{}, pgx.ErrNoRows
	}
	s.row.Status = "running"
	s.row.AttemptCount++
	return s.row, nil
}
func (s *dexTaskStore) GetDexOperation(context.Context, uuid.UUID) (sqlc.DexOperation, error) {
	return s.row, nil
}
func (s *dexTaskStore) MarkDexOperationSucceeded(context.Context, uuid.UUID) error {
	s.succeeded++
	s.row.Status = "succeeded"
	return nil
}
func (s *dexTaskStore) MarkDexOperationRetrying(context.Context, sqlc.MarkDexOperationRetryingParams) error {
	s.retrying++
	s.row.Status = "retrying"
	return nil
}
func (s *dexTaskStore) MarkDexOperationFailed(context.Context, sqlc.MarkDexOperationFailedParams) error {
	s.failed++
	s.row.Status = "failed"
	return nil
}

type dexTaskExecutor struct {
	calls int
	err   error
}

func (e *dexTaskExecutor) ExecuteDexOperation(context.Context, sqlc.DexOperation) error {
	e.calls++
	return e.err
}

func TestDexOperationRuntimeSuccessIsTerminalAndDuplicateSafe(t *testing.T) {
	id := uuid.New()
	store := &dexTaskStore{row: sqlc.DexOperation{ID: id, Status: "pending", CreatedAt: time.Now()}}
	executor := &dexTaskExecutor{}
	runtime := DexOperationRuntime{Queries: store, Executor: executor}
	task := asynq.NewTask(DexOperationType, []byte(`{"operation_id":"`+id.String()+`"}`))
	if err := runtime.Handle(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Handle(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if executor.calls != 1 || store.succeeded != 1 {
		t.Fatalf("calls/succeeded=%d/%d", executor.calls, store.succeeded)
	}
}

func TestDexOperationRuntimePersistsRetryWithoutLeakingCause(t *testing.T) {
	id := uuid.New()
	store := &dexTaskStore{row: sqlc.DexOperation{ID: id, Status: "pending"}}
	executor := &dexTaskExecutor{err: errors.New("credential-shaped private failure")}
	runtime := DexOperationRuntime{Queries: store, Executor: executor}
	task := asynq.NewTask(DexOperationType, []byte(`{"operation_id":"`+id.String()+`"}`))
	err := runtime.Handle(context.Background(), task)
	if err == nil || store.retrying != 1 || store.failed != 0 {
		t.Fatalf("err=%v retrying=%d failed=%d", err, store.retrying, store.failed)
	}
	if err.Error() != "Dex operation reconcile failed" {
		t.Fatalf("unsafe error=%q", err.Error())
	}
}
