package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const deleteClusterRegistryConfigByIDWithTaskOutbox = `
WITH deleted AS (
    DELETE FROM cluster_registry_configs
    WHERE id = $1
    RETURNING id
),
outbox AS (
    INSERT INTO task_outbox (
        dedupe_key, task_type, payload, queue_name, max_retry, timeout_seconds,
        unique_seconds, max_delivery_attempts, next_attempt_at
    )
    SELECT $2, $3, $4, $5, $6, $7, $8, $9, $10
    FROM deleted
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
SELECT 1 FROM deleted CROSS JOIN outbox
`

type DeleteClusterRegistryConfigByIDWithTaskOutboxParams struct {
	ID                  uuid.UUID          `json:"id"`
	DedupeKey           pgtype.Text        `json:"dedupe_key"`
	TaskType            string             `json:"task_type"`
	Payload             []byte             `json:"payload"`
	QueueName           string             `json:"queue_name"`
	MaxRetry            int32              `json:"max_retry"`
	TimeoutSeconds      int32              `json:"timeout_seconds"`
	UniqueSeconds       int32              `json:"unique_seconds"`
	MaxDeliveryAttempts int32              `json:"max_delivery_attempts"`
	NextAttemptAt       pgtype.Timestamptz `json:"next_attempt_at"`
}

func (q *Queries) DeleteClusterRegistryConfigByIDWithTaskOutbox(ctx context.Context, arg DeleteClusterRegistryConfigByIDWithTaskOutboxParams) error {
	var ok int
	return q.db.QueryRow(ctx, deleteClusterRegistryConfigByIDWithTaskOutbox,
		arg.ID,
		arg.DedupeKey,
		arg.TaskType,
		arg.Payload,
		arg.QueueName,
		arg.MaxRetry,
		arg.TimeoutSeconds,
		arg.UniqueSeconds,
		arg.MaxDeliveryAttempts,
		arg.NextAttemptAt,
	).Scan(&ok)
}
