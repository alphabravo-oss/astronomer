package tasks

import (
	"context"
	"math"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

type TaskOutboxWriter interface {
	UpsertTaskOutbox(ctx context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error)
}

type TaskOutboxOptions struct {
	DedupeKey           string
	QueueName           string
	MaxRetry            int
	Timeout             time.Duration
	Unique              time.Duration
	MaxDeliveryAttempts int
	NextAttemptAt       time.Time
}

func EnqueueTaskOutbox(ctx context.Context, q TaskOutboxWriter, task *asynq.Task, opts TaskOutboxOptions) (sqlc.TaskOutbox, error) {
	params, err := PrepareTaskOutbox(ctx, task, opts)
	if err != nil {
		return sqlc.TaskOutbox{}, err
	}
	return q.UpsertTaskOutbox(ctx, params)
}

// PrepareTaskOutbox shares validated durable parameters with transactional
// domain writers. It performs no database operation and never splits the
// caller's domain/task/audit transaction.
func PrepareTaskOutbox(ctx context.Context, task *asynq.Task, opts TaskOutboxOptions) (sqlc.UpsertTaskOutboxParams, error) {
	if task == nil {
		return sqlc.UpsertTaskOutboxParams{}, &jsoncontract.Error{Field: "task_payload", Category: "missing_task"}
	}
	payload, err := jsoncontract.ReadTaskPayloadV1(task.Payload())
	if err != nil {
		return sqlc.UpsertTaskOutboxParams{}, err
	}
	payload = observability.EnrichTaskPayload(ctx, payload, reqctx.CorrelationID(ctx))
	// Enrichment can increase the document size; validate the final durable bytes.
	if _, err := jsoncontract.ReadTaskPayloadV1(payload); err != nil {
		return sqlc.UpsertTaskOutboxParams{}, err
	}
	if opts.QueueName == "" {
		opts.QueueName = "default"
	}
	if opts.MaxRetry < 0 {
		opts.MaxRetry = 0
	}
	if opts.MaxDeliveryAttempts <= 0 {
		opts.MaxDeliveryAttempts = 20
	}
	timeoutSeconds := int64(opts.Timeout.Round(time.Second) / time.Second)
	uniqueSeconds := int64(opts.Unique.Round(time.Second) / time.Second)
	if opts.MaxRetry > math.MaxInt32 || opts.MaxDeliveryAttempts > math.MaxInt32 || timeoutSeconds < 0 || timeoutSeconds > math.MaxInt32 || uniqueSeconds < 0 || uniqueSeconds > math.MaxInt32 {
		return sqlc.UpsertTaskOutboxParams{}, &jsoncontract.Error{Field: "task_options", Category: "out_of_range"}
	}
	if opts.NextAttemptAt.IsZero() {
		opts.NextAttemptAt = time.Now().UTC()
	}
	dedupe := pgtype.Text{}
	if opts.DedupeKey != "" {
		dedupe = pgtype.Text{String: opts.DedupeKey, Valid: true}
	}
	return sqlc.UpsertTaskOutboxParams{
		DedupeKey:           dedupe,
		TaskType:            task.Type(),
		Payload:             payload,
		QueueName:           opts.QueueName,
		MaxRetry:            int32(opts.MaxRetry),
		TimeoutSeconds:      int32(timeoutSeconds),
		UniqueSeconds:       int32(uniqueSeconds),
		MaxDeliveryAttempts: int32(opts.MaxDeliveryAttempts),
		NextAttemptAt:       pgtype.Timestamptz{Time: opts.NextAttemptAt.UTC(), Valid: true},
	}, nil
}
