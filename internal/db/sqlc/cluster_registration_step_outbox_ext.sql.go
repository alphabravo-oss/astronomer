package sqlc

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const updateClusterRegistrationStepWithTaskOutbox = `
WITH step AS (
    UPDATE cluster_registration_steps
    SET status = $2,
        progress_pct = $3,
        detail_json = COALESCE($4, detail_json),
        started_at = COALESCE(started_at, $5),
        completed_at = $6,
        error_message = $7
    WHERE id = $1
    RETURNING id, cluster_id, step_name, label, status, progress_pct, detail_json, started_at, completed_at, error_message, created_at, step_order
),
outbox AS (
    INSERT INTO task_outbox (
        dedupe_key, task_type, payload, queue_name, max_retry, timeout_seconds,
        unique_seconds, max_delivery_attempts, next_attempt_at
    ) VALUES (
        $8, $9, $10, $11, $12, $13, $14, $15, $16
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
SELECT step.id, step.cluster_id, step.step_name, step.label, step.status, step.progress_pct, step.detail_json, step.started_at, step.completed_at, step.error_message, step.created_at, step.step_order
FROM step
CROSS JOIN outbox
`

type UpdateClusterRegistrationStepWithTaskOutboxParams struct {
	ID                  uuid.UUID          `json:"id"`
	Status              string             `json:"status"`
	ProgressPct         int32              `json:"progress_pct"`
	DetailJSON          json.RawMessage    `json:"detail_json"`
	StartedAt           pgtype.Timestamptz `json:"started_at"`
	CompletedAt         pgtype.Timestamptz `json:"completed_at"`
	ErrorMessage        string             `json:"error_message"`
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

func (q *Queries) UpdateClusterRegistrationStepWithTaskOutbox(ctx context.Context, arg UpdateClusterRegistrationStepWithTaskOutboxParams) (ClusterRegistrationStep, error) {
	row := q.db.QueryRow(ctx, updateClusterRegistrationStepWithTaskOutbox,
		arg.ID,
		arg.Status,
		arg.ProgressPct,
		arg.DetailJSON,
		arg.StartedAt,
		arg.CompletedAt,
		arg.ErrorMessage,
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
	var s ClusterRegistrationStep
	err := row.Scan(
		&s.ID,
		&s.ClusterID,
		&s.StepName,
		&s.Label,
		&s.Status,
		&s.ProgressPct,
		&s.DetailJSON,
		&s.StartedAt,
		&s.CompletedAt,
		&s.ErrorMessage,
		&s.CreatedAt,
		&s.StepOrder,
	)
	return s, err
}
