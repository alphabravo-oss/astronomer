package sqlc

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const upsertClusterTemplateApplicationWithTaskOutbox = `
WITH app AS (
    INSERT INTO cluster_template_applications (
        cluster_id, template_id, status, spec_snapshot, last_error, applied_at
    )
    VALUES ($1, $2, 'pending', $3, '', NULL)
    ON CONFLICT (cluster_id) DO UPDATE SET
        template_id   = EXCLUDED.template_id,
        status        = 'pending',
        spec_snapshot = EXCLUDED.spec_snapshot,
        last_error    = '',
        applied_at    = NULL,
        updated_at    = now()
    RETURNING cluster_id, template_id, status, spec_snapshot, last_error, applied_at, created_at, updated_at
),
outbox AS (
    INSERT INTO task_outbox (
        dedupe_key, task_type, payload, queue_name, max_retry, timeout_seconds,
        unique_seconds, max_delivery_attempts, next_attempt_at
    ) VALUES (
        $4, $5, $6, $7, $8, $9, $10, $11, $12
    )
    ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL DO UPDATE
    SET task_type             = EXCLUDED.task_type,
        payload               = EXCLUDED.payload,
        queue_name            = EXCLUDED.queue_name,
        max_retry             = EXCLUDED.max_retry,
        timeout_seconds       = EXCLUDED.timeout_seconds,
        unique_seconds        = EXCLUDED.unique_seconds,
        max_delivery_attempts = EXCLUDED.max_delivery_attempts,
        status                = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.status ELSE 'pending' END,
        next_attempt_at       = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.next_attempt_at ELSE EXCLUDED.next_attempt_at END,
        locked_until          = NULL,
        last_error            = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.last_error ELSE '' END,
        updated_at            = now()
    RETURNING id
)
SELECT app.cluster_id, app.template_id, app.status, app.spec_snapshot, app.last_error, app.applied_at, app.created_at, app.updated_at
FROM app
CROSS JOIN outbox
`

type UpsertClusterTemplateApplicationWithTaskOutboxParams struct {
	ClusterID           uuid.UUID       `json:"cluster_id"`
	TemplateID          uuid.UUID       `json:"template_id"`
	SpecSnapshot        json.RawMessage `json:"spec_snapshot"`
	DedupeKey           pgtype.Text     `json:"dedupe_key"`
	TaskType            string          `json:"task_type"`
	Payload             []byte          `json:"payload"`
	QueueName           string          `json:"queue_name"`
	MaxRetry            int32           `json:"max_retry"`
	TimeoutSeconds      int32           `json:"timeout_seconds"`
	UniqueSeconds       int32           `json:"unique_seconds"`
	MaxDeliveryAttempts int32           `json:"max_delivery_attempts"`
	NextAttemptAt       pgtype.Timestamptz
}

func (q *Queries) UpsertClusterTemplateApplicationWithTaskOutbox(ctx context.Context, arg UpsertClusterTemplateApplicationWithTaskOutboxParams) (ClusterTemplateApplication, error) {
	row := q.db.QueryRow(ctx, upsertClusterTemplateApplicationWithTaskOutbox,
		arg.ClusterID,
		arg.TemplateID,
		arg.SpecSnapshot,
		arg.DedupeKey,
		arg.TaskType,
		arg.Payload,
		arg.QueueName,
		arg.MaxRetry,
		arg.TimeoutSeconds,
		arg.UniqueSeconds,
		arg.MaxDeliveryAttempts,
		arg.NextAttemptAt,
	)
	var i ClusterTemplateApplication
	err := row.Scan(
		&i.ClusterID,
		&i.TemplateID,
		&i.Status,
		&i.SpecSnapshot,
		&i.LastError,
		&i.AppliedAt,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}
