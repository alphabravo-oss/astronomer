-- name: CreateToolOperation :one
INSERT INTO tool_operations (
    target_type,
    target_key,
    operation_type,
    payload,
    status,
    created_by_id
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetToolOperation :one
SELECT * FROM tool_operations WHERE id = $1;

-- name: ListToolOperations :many
SELECT * FROM tool_operations
WHERE (
    sqlc.narg(target_type)::text IS NULL OR target_type = sqlc.narg(target_type)::text
) AND (
    sqlc.narg(target_key)::text IS NULL OR target_key = sqlc.narg(target_key)::text
) AND (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
)
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListPendingToolOperations :many
SELECT * FROM tool_operations
WHERE status IN ('pending', 'running')
ORDER BY created_at ASC
LIMIT $1;

-- name: GetLatestToolOperationForTarget :one
SELECT * FROM tool_operations
WHERE target_type = $1 AND target_key = $2
ORDER BY created_at DESC
LIMIT 1;

-- name: MarkToolOperationRunning :one
-- Atomic claim (CORR-R01): only transition an op that is still claimable —
-- either 'pending', or a 'running' op whose lease is stale (started_at older
-- than the 1-minute fresh-running window). Under HA (server.replicaCount>1)
-- two reconcilers can ListPending the same row; the first UPDATE wins and the
-- second gets pgx.ErrNoRows so claimLatestOperations skips it.
UPDATE tool_operations
SET
    status = 'running',
    attempt_count = attempt_count + 1,
    started_at = now(),
    error_message = '',
    updated_at = now()
WHERE id = $1
  AND (
      status = 'pending'
      OR (status = 'running' AND (started_at IS NULL OR started_at < now() - interval '1 minute'))
  )
RETURNING *;

-- name: MarkToolOperationCompleted :one
UPDATE tool_operations
SET
    status = 'completed',
    completed_at = now(),
    error_message = '',
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkToolOperationFailed :one
UPDATE tool_operations
SET
    status = 'failed',
    completed_at = now(),
    error_message = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkToolOperationSuperseded :one
UPDATE tool_operations
SET
    status = 'superseded',
    completed_at = now(),
    error_message = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RequeueToolOperation :one
UPDATE tool_operations
SET
    status = 'pending',
    started_at = NULL,
    completed_at = NULL,
    error_message = '',
    updated_at = now()
WHERE id = $1
RETURNING *;
