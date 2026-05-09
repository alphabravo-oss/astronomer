-- name: CreateArgoCDOperation :one
INSERT INTO argocd_operations (
    target_type,
    target_key,
    operation_type,
    payload,
    status,
    created_by_id
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetArgoCDOperation :one
SELECT * FROM argocd_operations WHERE id = $1;

-- name: ListArgoCDOperations :many
SELECT * FROM argocd_operations
WHERE (
    sqlc.narg(target_type)::text IS NULL OR target_type = sqlc.narg(target_type)::text
) AND (
    sqlc.narg(target_key)::text IS NULL OR target_key = sqlc.narg(target_key)::text
) AND (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
)
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListPendingArgoCDOperations :many
SELECT * FROM argocd_operations
WHERE status IN ('pending', 'running')
ORDER BY created_at ASC
LIMIT $1;

-- name: GetLatestArgoCDOperationForTarget :one
SELECT * FROM argocd_operations
WHERE target_type = $1 AND target_key = $2
ORDER BY created_at DESC
LIMIT 1;

-- name: MarkArgoCDOperationRunning :one
UPDATE argocd_operations
SET
    status = 'running',
    attempt_count = attempt_count + 1,
    started_at = now(),
    error_message = '',
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkArgoCDOperationCompleted :one
UPDATE argocd_operations
SET
    status = 'completed',
    completed_at = now(),
    error_message = '',
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkArgoCDOperationFailed :one
UPDATE argocd_operations
SET
    status = 'failed',
    completed_at = now(),
    error_message = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkArgoCDOperationSuperseded :one
UPDATE argocd_operations
SET
    status = 'superseded',
    completed_at = now(),
    error_message = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RequeueArgoCDOperation :one
UPDATE argocd_operations
SET
    status = 'pending',
    started_at = NULL,
    completed_at = NULL,
    error_message = '',
    poll_attempts = 0,
    last_polled_at = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListRunningArgoCDOperations :many
SELECT * FROM argocd_operations
WHERE status = 'running'
ORDER BY started_at ASC NULLS FIRST
LIMIT $1;

-- name: UpdateArgoCDOperationProgress :one
UPDATE argocd_operations
SET
    phase          = $2,
    operation_id   = $3,
    revision       = $4,
    message        = $5,
    last_polled_at = now(),
    poll_attempts  = poll_attempts + 1,
    updated_at     = now()
WHERE id = $1
RETURNING *;

-- name: CompleteArgoCDOperationWithResult :one
UPDATE argocd_operations
SET
    status         = 'completed',
    phase          = $2,
    operation_id   = $3,
    revision       = $4,
    message        = $5,
    completed_at   = now(),
    error_message  = '',
    updated_at     = now()
WHERE id = $1
RETURNING *;

-- name: FailArgoCDOperationWithResult :one
UPDATE argocd_operations
SET
    status         = 'failed',
    phase          = $2,
    operation_id   = $3,
    revision       = $4,
    message        = $5,
    completed_at   = now(),
    error_message  = $6,
    updated_at     = now()
WHERE id = $1
RETURNING *;
