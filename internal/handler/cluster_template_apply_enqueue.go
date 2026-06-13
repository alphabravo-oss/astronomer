package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type clusterTemplateApplicationTaskOutboxQuerier interface {
	UpsertClusterTemplateApplicationWithTaskOutbox(ctx context.Context, arg sqlc.UpsertClusterTemplateApplicationWithTaskOutboxParams) (sqlc.ClusterTemplateApplication, error)
}

func enqueueClusterTemplateApplyOutbox(ctx context.Context, outbox tasks.TaskOutboxWriter, task *asynq.Task, clusterID uuid.UUID) bool {
	if outbox == nil || task == nil {
		return false
	}
	_, err := tasks.EnqueueTaskOutbox(ctx, outbox, task, tasks.TaskOutboxOptions{
		DedupeKey:           fmt.Sprintf("cluster_template_apply:%s", clusterID.String()),
		QueueName:           tasks.ClusterTemplateApplyQueueName,
		MaxRetry:            3,
		MaxDeliveryAttempts: 20,
	})
	return err == nil
}

func upsertClusterTemplateApplicationWithTaskOutbox(ctx context.Context, q any, outbox tasks.TaskOutboxWriter, app sqlc.UpsertClusterTemplateApplicationParams, task *asynq.Task, opts tasks.TaskOutboxOptions) (sqlc.ClusterTemplateApplication, bool, error) {
	atomicQ, ok := q.(clusterTemplateApplicationTaskOutboxQuerier)
	if !ok || outbox == nil || task == nil {
		return sqlc.ClusterTemplateApplication{}, false, nil
	}
	if opts.QueueName == "" {
		opts.QueueName = "default"
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
	row, err := atomicQ.UpsertClusterTemplateApplicationWithTaskOutbox(ctx, sqlc.UpsertClusterTemplateApplicationWithTaskOutboxParams{
		ClusterID:           app.ClusterID,
		TemplateID:          app.TemplateID,
		SpecSnapshot:        app.SpecSnapshot,
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
	return row, true, err
}
