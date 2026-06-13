package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const createClusterDecommissionWithTaskOutbox = `
WITH decom AS (
    INSERT INTO cluster_decommissions (id, cluster_id, status, requested_by_id, cluster_name)
    VALUES ($1, $2, 'pending', $3, $4)
    RETURNING id, cluster_id, status, phases, started_at, completed_at, last_error,
              attempts, requested_by_id, cluster_name, created_at, updated_at
),
outbox AS (
    INSERT INTO task_outbox (
        dedupe_key, task_type, payload, queue_name, max_retry, timeout_seconds,
        unique_seconds, max_delivery_attempts, next_attempt_at
    ) VALUES (
        $5, $6, $7, $8, $9, $10, $11, $12, $13
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
SELECT decom.id, decom.cluster_id, decom.status, decom.phases, decom.started_at, decom.completed_at, decom.last_error,
       decom.attempts, decom.requested_by_id, decom.cluster_name, decom.created_at, decom.updated_at
FROM decom
CROSS JOIN outbox
`

type CreateClusterDecommissionWithTaskOutboxParams struct {
	ID                  uuid.UUID          `json:"id"`
	ClusterID           uuid.UUID          `json:"cluster_id"`
	RequestedByID       pgtype.UUID        `json:"requested_by_id"`
	ClusterName         string             `json:"cluster_name"`
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

func (q *Queries) CreateClusterDecommissionWithTaskOutbox(ctx context.Context, arg CreateClusterDecommissionWithTaskOutboxParams) (ClusterDecommission, error) {
	row := q.db.QueryRow(ctx, createClusterDecommissionWithTaskOutbox,
		arg.ID,
		arg.ClusterID,
		arg.RequestedByID,
		arg.ClusterName,
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
	var i ClusterDecommission
	err := row.Scan(
		&i.ID,
		&i.ClusterID,
		&i.Status,
		&i.Phases,
		&i.StartedAt,
		&i.CompletedAt,
		&i.LastError,
		&i.Attempts,
		&i.RequestedByID,
		&i.ClusterName,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}
