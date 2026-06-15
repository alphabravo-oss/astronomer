package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const taskOutboxSelectColumns = `
    id, dedupe_key, task_type, payload, queue_name, max_retry,
    timeout_seconds, unique_seconds, max_delivery_attempts, status,
    attempt_count, next_attempt_at, locked_until, delivered_at,
    last_error, created_at, updated_at`

func scanTaskOutboxRow(row interface {
	Scan(dest ...any) error
}) (TaskOutbox, error) {
	var i TaskOutbox
	err := row.Scan(
		&i.ID,
		&i.DedupeKey,
		&i.TaskType,
		&i.Payload,
		&i.QueueName,
		&i.MaxRetry,
		&i.TimeoutSeconds,
		&i.UniqueSeconds,
		&i.MaxDeliveryAttempts,
		&i.Status,
		&i.AttemptCount,
		&i.NextAttemptAt,
		&i.LockedUntil,
		&i.DeliveredAt,
		&i.LastError,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const upsertTaskOutbox = `-- name: UpsertTaskOutbox :one
INSERT INTO task_outbox (
    dedupe_key, task_type, payload, queue_name, max_retry, timeout_seconds,
    unique_seconds, max_delivery_attempts, next_attempt_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
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
RETURNING ` + taskOutboxSelectColumns

type UpsertTaskOutboxParams struct {
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

func (q *Queries) UpsertTaskOutbox(ctx context.Context, arg UpsertTaskOutboxParams) (TaskOutbox, error) {
	return scanTaskOutboxRow(q.db.QueryRow(ctx, upsertTaskOutbox,
		arg.DedupeKey,
		arg.TaskType,
		arg.Payload,
		arg.QueueName,
		arg.MaxRetry,
		arg.TimeoutSeconds,
		arg.UniqueSeconds,
		arg.MaxDeliveryAttempts,
		arg.NextAttemptAt,
	))
}

const listTaskOutbox = `-- name: ListTaskOutbox :many
SELECT ` + taskOutboxSelectColumns + `
FROM task_outbox
WHERE ($1::text = '' OR status = $1)
ORDER BY updated_at DESC, created_at DESC
LIMIT $2 OFFSET $3`

type ListTaskOutboxParams struct {
	Status string `json:"status"`
	Limit  int32  `json:"limit"`
	Offset int32  `json:"offset"`
}

func (q *Queries) ListTaskOutbox(ctx context.Context, arg ListTaskOutboxParams) ([]TaskOutbox, error) {
	rows, err := q.db.Query(ctx, listTaskOutbox, arg.Status, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TaskOutbox{}
	for rows.Next() {
		i, err := scanTaskOutboxRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const countTaskOutbox = `-- name: CountTaskOutbox :one
SELECT COUNT(*)::bigint
FROM task_outbox
WHERE ($1::text = '' OR status = $1)`

func (q *Queries) CountTaskOutbox(ctx context.Context, status string) (int64, error) {
	var count int64
	err := q.db.QueryRow(ctx, countTaskOutbox, status).Scan(&count)
	return count, err
}

const getTaskOutbox = `-- name: GetTaskOutbox :one
SELECT ` + taskOutboxSelectColumns + `
FROM task_outbox
WHERE id = $1`

func (q *Queries) GetTaskOutbox(ctx context.Context, id uuid.UUID) (TaskOutbox, error) {
	return scanTaskOutboxRow(q.db.QueryRow(ctx, getTaskOutbox, id))
}

const claimDueTaskOutbox = `-- name: ClaimDueTaskOutbox :many
WITH picked AS (
    SELECT id
    FROM task_outbox
    WHERE status IN ('pending', 'failed', 'delivering')
      AND next_attempt_at <= $1
      AND (locked_until IS NULL OR locked_until <= $1)
    ORDER BY next_attempt_at ASC, created_at ASC
    LIMIT $3
    FOR UPDATE SKIP LOCKED
)
UPDATE task_outbox AS t
SET status        = 'delivering',
    locked_until  = $2,
    attempt_count = attempt_count + 1,
    updated_at    = now()
FROM picked
WHERE t.id = picked.id
RETURNING ` + taskOutboxSelectColumns

type ClaimDueTaskOutboxParams struct {
	Now         pgtype.Timestamptz `json:"now"`
	LockedUntil pgtype.Timestamptz `json:"locked_until"`
	Limit       int32              `json:"limit"`
}

func (q *Queries) ClaimDueTaskOutbox(ctx context.Context, arg ClaimDueTaskOutboxParams) ([]TaskOutbox, error) {
	rows, err := q.db.Query(ctx, claimDueTaskOutbox, arg.Now, arg.LockedUntil, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TaskOutbox{}
	for rows.Next() {
		i, err := scanTaskOutboxRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const markTaskOutboxDelivered = `-- name: MarkTaskOutboxDelivered :exec
UPDATE task_outbox
SET status = 'delivered',
    delivered_at = $2,
    locked_until = NULL,
    last_error = '',
    updated_at = now()
WHERE id = $1`

type MarkTaskOutboxDeliveredParams struct {
	ID          uuid.UUID          `json:"id"`
	DeliveredAt pgtype.Timestamptz `json:"delivered_at"`
}

func (q *Queries) MarkTaskOutboxDelivered(ctx context.Context, arg MarkTaskOutboxDeliveredParams) error {
	_, err := q.db.Exec(ctx, markTaskOutboxDelivered, arg.ID, arg.DeliveredAt)
	return err
}

const markTaskOutboxFailed = `-- name: MarkTaskOutboxFailed :exec
UPDATE task_outbox
SET status = $2,
    next_attempt_at = $3,
    locked_until = NULL,
    last_error = $4,
    updated_at = now()
WHERE id = $1`

type MarkTaskOutboxFailedParams struct {
	ID            uuid.UUID          `json:"id"`
	Status        string             `json:"status"`
	NextAttemptAt pgtype.Timestamptz `json:"next_attempt_at"`
	LastError     string             `json:"last_error"`
}

func (q *Queries) MarkTaskOutboxFailed(ctx context.Context, arg MarkTaskOutboxFailedParams) error {
	_, err := q.db.Exec(ctx, markTaskOutboxFailed, arg.ID, arg.Status, arg.NextAttemptAt, arg.LastError)
	return err
}

const retryTaskOutbox = `-- name: RetryTaskOutbox :one
UPDATE task_outbox
SET status = 'pending',
    next_attempt_at = $2,
    locked_until = NULL,
    last_error = '',
    updated_at = now()
WHERE id = $1
  AND status <> 'delivered'
RETURNING ` + taskOutboxSelectColumns

type RetryTaskOutboxParams struct {
	ID            uuid.UUID          `json:"id"`
	NextAttemptAt pgtype.Timestamptz `json:"next_attempt_at"`
}

func (q *Queries) RetryTaskOutbox(ctx context.Context, arg RetryTaskOutboxParams) (TaskOutbox, error) {
	row, err := scanTaskOutboxRow(q.db.QueryRow(ctx, retryTaskOutbox, arg.ID, arg.NextAttemptAt))
	if err == pgx.ErrNoRows {
		return TaskOutbox{}, pgx.ErrNoRows
	}
	return row, err
}
