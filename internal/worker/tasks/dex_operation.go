package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const (
	DexOperationType         = "dex:apply_operation"
	DexOperationRecoveryType = "dex:recover_operations"
)

type DexOperationPayload struct {
	OperationID string `json:"operation_id"`
}

type DexOperationQuerier interface {
	ClaimDexOperation(context.Context, sqlc.ClaimDexOperationParams) (sqlc.DexOperation, error)
	GetDexOperation(context.Context, uuid.UUID) (sqlc.DexOperation, error)
	MarkDexOperationSucceeded(context.Context, uuid.UUID) error
	MarkDexOperationRetrying(context.Context, sqlc.MarkDexOperationRetryingParams) error
	MarkDexOperationFailed(context.Context, sqlc.MarkDexOperationFailedParams) error
	RecoverDexOperationOutbox(context.Context, time.Time) (int64, error)
}

type DexOperationExecutor interface {
	ExecuteDexOperation(context.Context, sqlc.DexOperation) error
}

type DexOperationRuntime struct {
	Queries  DexOperationQuerier
	Executor DexOperationExecutor
}

func (runtime DexOperationRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if runtime.Queries == nil || runtime.Executor == nil {
		return nil, errors.New("Dex operation runtime is not configured")
	}
	return map[string]asynq.HandlerFunc{DexOperationType: runtime.Handle, DexOperationRecoveryType: runtime.HandleRecovery}, nil
}

// HandleRecovery reopens delivered outbox intents whose durable operation has
// made no progress. It makes a complete Redis loss recoverable from Postgres.
func (runtime DexOperationRuntime) HandleRecovery(ctx context.Context, _ *asynq.Task) error {
	_, err := runtime.Queries.RecoverDexOperationOutbox(ctx, time.Now().UTC().Add(-5*time.Minute))
	if err != nil {
		return errors.New("recover Dex operation outbox")
	}
	return nil
}

func (runtime DexOperationRuntime) Handle(ctx context.Context, task *asynq.Task) error {
	if task == nil {
		return asynq.SkipRetry
	}
	var payload DexOperationPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: invalid Dex operation payload", asynq.SkipRetry)
	}
	id, err := uuid.Parse(payload.OperationID)
	if err != nil {
		return fmt.Errorf("%w: invalid Dex operation id", asynq.SkipRetry)
	}
	operation, err := runtime.Queries.ClaimDexOperation(ctx, sqlc.ClaimDexOperationParams{
		ID: id, LockedUntil: pgtype.Timestamptz{Time: time.Now().UTC().Add(3 * time.Minute), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := runtime.Queries.GetDexOperation(ctx, id)
		if errors.Is(loadErr, pgx.ErrNoRows) {
			return nil
		}
		if loadErr != nil {
			return errors.New("load Dex operation failed")
		}
		if existing.Status == "succeeded" || existing.Status == "failed" {
			return nil
		}
		return errors.New("Dex operation lease is held")
	}
	if err != nil {
		return errors.New("claim Dex operation failed")
	}
	if err := runtime.Executor.ExecuteDexOperation(ctx, operation); err == nil {
		if markErr := runtime.Queries.MarkDexOperationSucceeded(ctx, id); markErr != nil {
			return errors.New("mark Dex operation succeeded")
		}
		return nil
	} else {
		terminal := errors.Is(err, asynq.SkipRetry) || dexRetriesExhausted(ctx)
		if terminal {
			if markErr := runtime.Queries.MarkDexOperationFailed(ctx, sqlc.MarkDexOperationFailedParams{ID: id, ErrorCode: "reconcile_failed"}); markErr != nil {
				return errors.New("mark Dex operation failed")
			}
			return fmt.Errorf("%w: Dex operation failed", asynq.SkipRetry)
		}
		if markErr := runtime.Queries.MarkDexOperationRetrying(ctx, sqlc.MarkDexOperationRetryingParams{ID: id, ErrorCode: "reconcile_retrying"}); markErr != nil {
			return errors.New("mark Dex operation retrying")
		}
		return errors.New("Dex operation reconcile failed")
	}
}

func dexRetriesExhausted(ctx context.Context) bool {
	retried, retryOK := asynq.GetRetryCount(ctx)
	maximum, maximumOK := asynq.GetMaxRetry(ctx)
	return retryOK && maximumOK && retried >= maximum
}
