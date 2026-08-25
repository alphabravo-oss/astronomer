-- name: CreateAdminQueueOperation :one
INSERT INTO admin_queue_operations (
    action, queue_name, task_id, requested_by
) VALUES (
    sqlc.arg(action), sqlc.arg(queue_name), sqlc.arg(task_id), sqlc.arg(requested_by)
)
ON CONFLICT (queue_name, task_id)
    WHERE status IN ('pending', 'running', 'retrying')
DO UPDATE SET updated_at = admin_queue_operations.updated_at
RETURNING *;

-- name: GetAdminQueueOperation :one
SELECT *
FROM admin_queue_operations
WHERE id = sqlc.arg(id);

-- name: ClaimAdminQueueOperation :one
UPDATE admin_queue_operations
SET status = 'running',
    attempt_count = attempt_count + 1,
    last_error = '',
    locked_until = sqlc.arg(locked_until),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND (
      status IN ('pending', 'retrying')
      OR (status = 'running' AND (locked_until IS NULL OR locked_until <= sqlc.arg(now)))
  )
RETURNING *;

-- name: MarkAdminQueueOperationEffectStarted :one
-- This durable phase is written before Redis mutation. A retry target that is
-- absent before this phase never existed; absence after this phase is a
-- converged outcome because RunTask may have succeeded and been consumed.
UPDATE admin_queue_operations
SET effect_started_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)
  AND status = 'running'
  AND effect_started_at IS NULL
RETURNING *;

-- name: MarkAdminQueueOperationRetrying :one
UPDATE admin_queue_operations
SET status = 'retrying',
    last_error = left(sqlc.arg(last_error), 2048),
    locked_until = NULL,
    completed_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND status NOT IN ('failed', 'succeeded')
RETURNING *;

-- name: MarkAdminQueueOperationSucceeded :one
UPDATE admin_queue_operations
SET status = 'succeeded',
    last_error = '',
    locked_until = NULL,
    completed_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND status <> 'succeeded'
RETURNING *;

-- name: MarkAdminQueueOperationFailed :one
UPDATE admin_queue_operations
SET status = 'failed',
    last_error = left(sqlc.arg(last_error), 2048),
    locked_until = NULL,
    completed_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND status <> 'succeeded'
RETURNING *;
