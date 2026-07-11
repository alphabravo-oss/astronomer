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

-- name: CountArgoCDOperations :one
SELECT COUNT(*)::bigint FROM argocd_operations
WHERE (
    sqlc.narg(target_type)::text IS NULL OR target_type = sqlc.narg(target_type)::text
) AND (
    sqlc.narg(target_key)::text IS NULL OR target_key = sqlc.narg(target_key)::text
) AND (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
);

-- name: ListPendingArgoCDOperations :many
SELECT * FROM argocd_operations
WHERE status = 'pending'
ORDER BY created_at ASC
LIMIT $1;

-- name: GetLatestArgoCDOperationForTarget :one
SELECT * FROM argocd_operations
WHERE target_type = $1 AND target_key = $2
ORDER BY created_at DESC
LIMIT 1;

-- name: MarkArgoCDOperationRunning :one
-- Atomic at-most-once dispatch claim. Running Argo operations are asynchronous
-- and are resumed exclusively by ClaimRunningArgoCDOperationsForPoll; replaying
-- the mutation after a local lease expires restarts upstream hooks and can
-- duplicate side effects. Under HA, only one replica can transition pending to
-- running; all competing claimers receive pgx.ErrNoRows.
UPDATE argocd_operations
SET
    status = 'running',
    attempt_count = attempt_count + 1,
    started_at = now(),
    error_message = '',
    updated_at = now()
WHERE id = $1
  AND status = 'pending'
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
    payload = $2,
    status = 'pending',
    started_at = NULL,
    completed_at = NULL,
    error_message = '',
    poll_attempts = 0,
    last_polled_at = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ClaimRunningArgoCDOperationsForPoll :many
-- Claim a bounded poll batch atomically across server replicas. The 25-second
-- lease is shorter than the 30-second reconciler cadence and longer than the
-- 10-second upstream client timeout. poll_attempts is charged once at claim
-- time so replica count cannot accelerate the timeout budget.
WITH eligible AS (
    SELECT id
    FROM argocd_operations
    WHERE status = 'running'
      AND (last_polled_at IS NULL OR last_polled_at < now() - interval '25 seconds')
    ORDER BY started_at ASC NULLS FIRST
    FOR UPDATE SKIP LOCKED
    LIMIT $1
)
UPDATE argocd_operations AS operation
SET
    last_polled_at = now(),
    poll_attempts  = operation.poll_attempts + 1,
    updated_at     = now()
FROM eligible
WHERE operation.id = eligible.id
  AND operation.status = 'running'
RETURNING operation.*;

-- name: UpdateArgoCDOperationProgress :one
UPDATE argocd_operations
SET
    phase          = $2,
    operation_id   = $3,
    revision       = $4,
    message        = $5,
    updated_at     = now()
WHERE id = $1
  AND status = 'running'
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
  AND status = 'running'
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
  AND status = 'running'
RETURNING *;
