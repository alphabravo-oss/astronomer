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

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const (
	AuditExportOperationType    = "audit_export:generate"
	AuditExportRecoveryType     = "audit_export:recover"
	maxAuditExportArtifactBytes = 256 * 1024 * 1024
	auditExportTimeout          = 14 * time.Minute
)

var errAuditExportArtifactTooLarge = errors.New("audit export artifact exceeds 256 MiB")

type AuditExportOperationQuerier interface {
	ClaimAuditExportOperation(context.Context, sqlc.ClaimAuditExportOperationParams) (sqlc.AuditExportOperation, error)
	GetAuditExportOperation(context.Context, uuid.UUID) (sqlc.GetAuditExportOperationRow, error)
	MarkAuditExportOperationSucceeded(context.Context, sqlc.MarkAuditExportOperationSucceededParams) (sqlc.AuditExportOperation, error)
	MarkAuditExportOperationRetrying(context.Context, sqlc.MarkAuditExportOperationRetryingParams) (sqlc.AuditExportOperation, error)
	MarkAuditExportOperationFailed(context.Context, sqlc.MarkAuditExportOperationFailedParams) (sqlc.AuditExportOperation, error)
	RecoverAuditExportOperationOutbox(context.Context, time.Time) (int64, error)
}

type AuditExportGenerator interface {
	GenerateAuditExport(context.Context, audit.ExportSpec, io.Writer) error
}

type AuditExportRuntime struct {
	Queries   AuditExportOperationQuerier
	Generator AuditExportGenerator
	Now       func() time.Time
}

func (runtime AuditExportRuntime) Validate() error {
	return validateRequiredRuntimeDependencies("audit export", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Queries},
		{name: "generator", value: runtime.Generator},
	})
}

func (runtime AuditExportRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := runtime.Validate(); err != nil {
		return nil, err
	}
	return map[string]asynq.HandlerFunc{
		AuditExportOperationType: runtime.HandleGenerate,
		AuditExportRecoveryType:  runtime.HandleRecovery,
	}, nil
}

func NewAuditExportRecoveryTask() *asynq.Task {
	return asynq.NewTask(AuditExportRecoveryType, nil, asynq.MaxRetry(2))
}

func (runtime AuditExportRuntime) HandleRecovery(ctx context.Context, _ *asynq.Task) error {
	if err := runtime.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if runtime.Now != nil {
		now = runtime.Now().UTC()
	}
	_, err := runtime.Queries.RecoverAuditExportOperationOutbox(ctx, now.Add(-2*time.Minute))
	if err != nil {
		return fmt.Errorf("recover audit export task intents: %w", err)
	}
	return nil
}

type AuditExportOperationPayload struct {
	OperationID string `json:"operation_id"`
}

func NewAuditExportOperationTask(operationID uuid.UUID) (*asynq.Task, error) {
	if operationID == uuid.Nil {
		return nil, errors.New("audit export operation ID is required")
	}
	payload, err := json.Marshal(AuditExportOperationPayload{OperationID: operationID.String()})
	if err != nil {
		return nil, fmt.Errorf("marshal audit export operation: %w", err)
	}
	return asynq.NewTask(AuditExportOperationType, payload, asynq.MaxRetry(4), asynq.Timeout(15*time.Minute)), nil
}

func (runtime AuditExportRuntime) HandleGenerate(ctx context.Context, task *asynq.Task) error {
	if task == nil {
		return fmt.Errorf("audit export task is required: %w", asynq.SkipRetry)
	}
	var payload AuditExportOperationPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode audit export task: %w: %w", err, asynq.SkipRetry)
	}
	id, err := uuid.Parse(payload.OperationID)
	if err != nil {
		return fmt.Errorf("invalid audit export operation ID: %w", asynq.SkipRetry)
	}
	return runtime.generate(ctx, id)
}

func (runtime AuditExportRuntime) generate(ctx context.Context, id uuid.UUID) error {
	if err := runtime.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if runtime.Now != nil {
		now = runtime.Now().UTC()
	}
	row, err := runtime.Queries.ClaimAuditExportOperation(ctx, sqlc.ClaimAuditExportOperationParams{
		ID: id, LockedUntil: pgtype.Timestamptz{Time: now.Add(15 * time.Minute), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		current, getErr := runtime.Queries.GetAuditExportOperation(ctx, id)
		if getErr == nil && current.Status == "succeeded" {
			return nil
		}
		if getErr == nil && !current.ExpiresAt.After(now) {
			return fmt.Errorf("audit export operation expired: %w", asynq.SkipRetry)
		}
		if getErr == nil && current.Status == "running" {
			return errors.New("audit export operation lease is held")
		}
		if getErr == nil {
			return fmt.Errorf("audit export operation is not recoverable from status %q: %w", current.Status, asynq.SkipRetry)
		}
	}
	if err != nil {
		return fmt.Errorf("claim audit export operation: %w", err)
	}

	var spec audit.ExportSpec
	if err := json.Unmarshal(row.RequestSpec, &spec); err != nil {
		_, _ = runtime.Queries.MarkAuditExportOperationFailed(ctx, sqlc.MarkAuditExportOperationFailedParams{ErrorCode: "invalid_request", ID: row.ID})
		return fmt.Errorf("decode audit export request: %w: %w", err, asynq.SkipRetry)
	}
	generateCtx, cancel := context.WithTimeout(ctx, auditExportTimeout)
	defer cancel()
	artifact := &auditExportBuffer{limit: maxAuditExportArtifactBytes}
	generateErr := runtime.Generator.GenerateAuditExport(generateCtx, spec, artifact)
	if generateErr == nil {
		digest := sha256.Sum256(artifact.Bytes())
		_, err = runtime.Queries.MarkAuditExportOperationSucceeded(ctx, sqlc.MarkAuditExportOperationSucceededParams{
			Filename: "astronomer-audit-export-" + now.Format("20060102-150405") + ".csv",
			Artifact: artifact.Bytes(), ArtifactSha256: pgtype.Text{String: fmt.Sprintf("%x", digest[:]), Valid: true}, ID: row.ID,
		})
		if err != nil {
			return fmt.Errorf("persist audit export artifact: %w", err)
		}
		return nil
	}

	terminal := errors.Is(generateErr, errAuditExportArtifactTooLarge) || supportBundleRetriesExhausted(ctx)
	errorCode := auditExportErrorCode(generateErr)
	if terminal {
		_, err = runtime.Queries.MarkAuditExportOperationFailed(ctx, sqlc.MarkAuditExportOperationFailedParams{ErrorCode: errorCode, ID: row.ID})
	} else {
		_, err = runtime.Queries.MarkAuditExportOperationRetrying(ctx, sqlc.MarkAuditExportOperationRetryingParams{ErrorCode: errorCode, ID: row.ID})
	}
	if err != nil {
		return fmt.Errorf("persist audit export failure: %w", err)
	}
	safeErr := errors.New("audit export generation failed: " + errorCode)
	if terminal {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, safeErr)
	}
	return safeErr
}

func auditExportErrorCode(err error) string {
	switch {
	case errors.Is(err, errAuditExportArtifactTooLarge):
		return "artifact_too_large"
	case errors.Is(err, context.DeadlineExceeded):
		return "generation_timeout"
	case errors.Is(err, context.Canceled):
		return "generation_canceled"
	default:
		return "generation_failed"
	}
}

type auditExportBuffer struct {
	bytes.Buffer
	limit int
}

func (w *auditExportBuffer) Write(p []byte) (int, error) {
	remaining := w.limit - w.Len()
	if remaining <= 0 {
		return 0, errAuditExportArtifactTooLarge
	}
	if len(p) > remaining {
		_, _ = w.Buffer.Write(p[:remaining])
		return remaining, errAuditExportArtifactTooLarge
	}
	return w.Buffer.Write(p)
}
