-- name: CreateMonitoringOperation :one
INSERT INTO monitoring_operations (
    target_type,
    target_key,
    operation_type,
    payload,
    status,
    created_by_id
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetMonitoringOperation :one
SELECT * FROM monitoring_operations WHERE id = $1;

-- name: ListMonitoringOperations :many
SELECT * FROM monitoring_operations
WHERE (
    sqlc.narg(target_type)::text IS NULL OR target_type = sqlc.narg(target_type)::text
) AND (
    sqlc.narg(target_key)::text IS NULL OR target_key = sqlc.narg(target_key)::text
) AND (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
)
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListPendingMonitoringOperations :many
SELECT * FROM monitoring_operations
WHERE status IN ('pending', 'running')
ORDER BY created_at ASC
LIMIT $1;

-- name: GetLatestMonitoringOperationForTarget :one
SELECT * FROM monitoring_operations
WHERE target_type = $1 AND target_key = $2
ORDER BY created_at DESC
LIMIT 1;

-- name: MarkMonitoringOperationRunning :one
UPDATE monitoring_operations
SET
    status = 'running',
    attempt_count = attempt_count + 1,
    started_at = now(),
    error_message = '',
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkMonitoringOperationCompleted :one
UPDATE monitoring_operations
SET
    status = 'completed',
    completed_at = now(),
    error_message = '',
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkMonitoringOperationFailed :one
UPDATE monitoring_operations
SET
    status = 'failed',
    completed_at = now(),
    error_message = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkMonitoringOperationSuperseded :one
UPDATE monitoring_operations
SET
    status = 'superseded',
    completed_at = now(),
    error_message = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RequeueMonitoringOperation :one
UPDATE monitoring_operations
SET
    status = 'pending',
    started_at = NULL,
    completed_at = NULL,
    error_message = '',
    updated_at = now()
WHERE id = $1
RETURNING *;
