package tasks

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
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
	if opts.QueueName == "" {
		opts.QueueName = "default"
	}
	if opts.MaxRetry < 0 {
		opts.MaxRetry = 0
	}
	if opts.MaxDeliveryAttempts <= 0 {
		opts.MaxDeliveryAttempts = 20
	}
	if opts.NextAttemptAt.IsZero() {
		opts.NextAttemptAt = time.Now().UTC()
	}
	dedupe := pgtype.Text{}
	if opts.DedupeKey != "" {
		dedupe = pgtype.Text{String: opts.DedupeKey, Valid: true}
	}
	return q.UpsertTaskOutbox(ctx, sqlc.UpsertTaskOutboxParams{
		DedupeKey:           dedupe,
		TaskType:            task.Type(),
		Payload:             task.Payload(),
		QueueName:           opts.QueueName,
		MaxRetry:            int32(opts.MaxRetry),
		TimeoutSeconds:      int32(opts.Timeout.Round(time.Second) / time.Second),
		UniqueSeconds:       int32(opts.Unique.Round(time.Second) / time.Second),
		MaxDeliveryAttempts: int32(opts.MaxDeliveryAttempts),
		NextAttemptAt:       pgtype.Timestamptz{Time: opts.NextAttemptAt.UTC(), Valid: true},
	})
}
