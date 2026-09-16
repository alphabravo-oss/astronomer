package tasks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const (
	SupportBundleOperationType     = "support_bundle:generate"
	SupportBundleRecoveryType      = "support_bundle:recover"
	maxSupportBundleArtifactBytes  = 64 * 1024 * 1024
	supportBundleGenerationTimeout = 9 * time.Minute
)

var errSupportBundleArtifactTooLarge = errors.New("support bundle artifact exceeds 64 MiB")

type SupportBundleGenerator interface {
	Generate(context.Context, io.Writer) error
}

type SupportBundleOperationQuerier interface {
	ClaimSupportBundleOperation(context.Context, sqlc.ClaimSupportBundleOperationParams) (sqlc.SupportBundleOperation, error)
	GetSupportBundleOperation(context.Context, uuid.UUID) (sqlc.GetSupportBundleOperationRow, error)
	MarkSupportBundleOperationSucceeded(context.Context, sqlc.MarkSupportBundleOperationSucceededParams) (sqlc.SupportBundleOperation, error)
	MarkSupportBundleOperationRetrying(context.Context, sqlc.MarkSupportBundleOperationRetryingParams) (sqlc.SupportBundleOperation, error)
	MarkSupportBundleOperationFailed(context.Context, sqlc.MarkSupportBundleOperationFailedParams) (sqlc.SupportBundleOperation, error)
	RecoverSupportBundleOperationOutbox(context.Context, time.Time) (int64, error)
}

type SupportBundleRuntime struct {
	Queries           SupportBundleOperationQuerier
	Generator         SupportBundleGenerator
	Now               func() time.Time
	GenerationTimeout time.Duration
}

func (runtime SupportBundleRuntime) Validate() error {
	return validateRequiredRuntimeDependencies("support bundle", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Queries},
		{name: "generator", value: runtime.Generator},
	})
}

func (runtime SupportBundleRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := runtime.Validate(); err != nil {
		return nil, err
	}
	return map[string]asynq.HandlerFunc{
		SupportBundleOperationType: runtime.HandleGenerate,
		SupportBundleRecoveryType:  runtime.HandleRecovery,
	}, nil
}

func NewSupportBundleRecoveryTask() *asynq.Task {
	return asynq.NewTask(SupportBundleRecoveryType, nil, asynq.MaxRetry(2))
}

func (runtime SupportBundleRuntime) HandleRecovery(ctx context.Context, _ *asynq.Task) error {
	if err := runtime.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if runtime.Now != nil {
		now = runtime.Now().UTC()
	}
	_, err := runtime.Queries.RecoverSupportBundleOperationOutbox(ctx, now.Add(-2*time.Minute))
	if err != nil {
		return fmt.Errorf("recover support bundle task intents: %w", err)
	}
	return nil
}

type SupportBundleOperationPayload struct {
	OperationID string `json:"operation_id"`
}

func NewSupportBundleOperationTask(operationID uuid.UUID) (*asynq.Task, error) {
	if operationID == uuid.Nil {
		return nil, errors.New("support bundle operation ID is required")
	}
	payload, err := json.Marshal(SupportBundleOperationPayload{OperationID: operationID.String()})
	if err != nil {
		return nil, fmt.Errorf("marshal support bundle operation: %w", err)
	}
	return asynq.NewTask(SupportBundleOperationType, payload, asynq.MaxRetry(4), asynq.Timeout(10*time.Minute)), nil
}

func (runtime SupportBundleRuntime) HandleGenerate(ctx context.Context, task *asynq.Task) error {
	if task == nil {
		return fmt.Errorf("support bundle task is required: %w", asynq.SkipRetry)
	}
	var payload SupportBundleOperationPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode support bundle task: %w: %w", err, asynq.SkipRetry)
	}
	id, err := uuid.Parse(payload.OperationID)
	if err != nil {
		return fmt.Errorf("invalid support bundle operation ID: %w", asynq.SkipRetry)
	}
	return runtime.generate(ctx, id)
}

func (runtime SupportBundleRuntime) generate(ctx context.Context, id uuid.UUID) error {
	if err := runtime.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if runtime.Now != nil {
		now = runtime.Now().UTC()
	}
	row, err := runtime.Queries.ClaimSupportBundleOperation(ctx, sqlc.ClaimSupportBundleOperationParams{
		ID: id, LockedUntil: pgtype.Timestamptz{Time: now.Add(10 * time.Minute), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		current, getErr := runtime.Queries.GetSupportBundleOperation(ctx, id)
		if getErr == nil && current.Status == "succeeded" {
			return nil
		}
		if getErr == nil && !current.ExpiresAt.After(now) {
			return fmt.Errorf("support bundle operation expired: %w", asynq.SkipRetry)
		}
		if getErr == nil && current.Status == "running" {
			return errors.New("support bundle operation lease is held")
		}
		if getErr == nil {
			return fmt.Errorf("support bundle operation is not recoverable from status %q: %w", current.Status, asynq.SkipRetry)
		}
	}
	if err != nil {
		return fmt.Errorf("claim support bundle operation: %w", err)
	}

	timeout := runtime.GenerationTimeout
	if timeout <= 0 {
		timeout = supportBundleGenerationTimeout
	}
	generateCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	artifact := &boundedBuffer{limit: maxSupportBundleArtifactBytes}
	generateErr := runtime.Generator.Generate(generateCtx, artifact)
	if generateErr == nil {
		digest := sha256.Sum256(artifact.Bytes())
		_, err = runtime.Queries.MarkSupportBundleOperationSucceeded(ctx, sqlc.MarkSupportBundleOperationSucceededParams{
			Filename:       "astronomer-support-bundle-" + now.Format("20060102-150405") + ".zip",
			Artifact:       artifact.Bytes(),
			ArtifactSha256: pgtype.Text{String: fmt.Sprintf("%x", digest[:]), Valid: true},
			ID:             row.ID,
		})
		if err != nil {
			return fmt.Errorf("persist support bundle artifact: %w", err)
		}
		return nil
	}

	terminal := errors.Is(generateErr, errSupportBundleArtifactTooLarge) || supportBundleRetriesExhausted(ctx)
	errorCode := supportBundleErrorCode(generateErr)
	if terminal {
		_, err = runtime.Queries.MarkSupportBundleOperationFailed(ctx, sqlc.MarkSupportBundleOperationFailedParams{ErrorCode: errorCode, ID: row.ID})
	} else {
		_, err = runtime.Queries.MarkSupportBundleOperationRetrying(ctx, sqlc.MarkSupportBundleOperationRetryingParams{ErrorCode: errorCode, ID: row.ID})
	}
	if err != nil {
		return fmt.Errorf("persist support bundle failure: %w", err)
	}
	safeErr := errors.New("support bundle generation failed: " + errorCode)
	if terminal {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, safeErr)
	}
	return safeErr
}

func supportBundleRetriesExhausted(ctx context.Context) bool {
	retried, retryOK := asynq.GetRetryCount(ctx)
	maximum, maximumOK := asynq.GetMaxRetry(ctx)
	return retryOK && maximumOK && retried >= maximum
}

func supportBundleErrorCode(err error) string {
	switch {
	case errors.Is(err, errSupportBundleArtifactTooLarge):
		return "artifact_too_large"
	case errors.Is(err, context.DeadlineExceeded):
		return "generation_timeout"
	case errors.Is(err, context.Canceled):
		return "generation_canceled"
	default:
		return "generation_failed"
	}
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (w *boundedBuffer) Write(p []byte) (int, error) {
	remaining := w.limit - w.Len()
	if remaining <= 0 {
		return 0, errSupportBundleArtifactTooLarge
	}
	if len(p) > remaining {
		_, _ = w.Buffer.Write(p[:remaining])
		return remaining, errSupportBundleArtifactTooLarge
	}
	return w.Buffer.Write(p)
}
