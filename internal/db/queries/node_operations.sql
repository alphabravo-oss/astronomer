-- name: CreateNodeOperationIdempotent :one
INSERT INTO node_operations (
    idempotency_scope, idempotency_key, request_digest, cluster_id,
    node_name, action, parameters_encrypted, created_by_id
) VALUES (
    sqlc.arg(idempotency_scope), sqlc.arg(idempotency_key), sqlc.arg(request_digest),
    sqlc.arg(cluster_id), sqlc.arg(node_name), sqlc.arg(action),
    sqlc.arg(parameters_encrypted), sqlc.narg(created_by_id)
)
ON CONFLICT (idempotency_scope, idempotency_key) DO UPDATE
SET updated_at = node_operations.updated_at
RETURNING *;

-- name: GetNodeOperation :one
SELECT * FROM node_operations WHERE id = $1;

-- name: ClaimNodeOperationGeneration :one
UPDATE node_operations
SET status = 'running', attempt_count = attempt_count + 1,
    locked_until = sqlc.arg(locked_until), error_code = '', updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND generation = sqlc.arg(generation)
  AND observed_generation < sqlc.arg(generation)
  AND (
      status IN ('pending', 'retrying')
      OR (status = 'running' AND (locked_until IS NULL OR locked_until < sqlc.arg(now)))
  )
RETURNING *;

-- name: UpdateNodeOperationProgress :one
UPDATE node_operations
SET progress = sqlc.arg(progress), updated_at = now()
WHERE id = sqlc.arg(id) AND generation = sqlc.arg(generation)
  AND observed_generation < sqlc.arg(generation) AND status = 'running'
RETURNING *;

-- name: MarkNodeOperationSucceeded :one
UPDATE node_operations
SET status = 'succeeded', observed_generation = sqlc.arg(generation),
    progress = sqlc.arg(progress), error_code = '', locked_until = NULL,
    completed_at = now(), updated_at = now()
WHERE id = sqlc.arg(id) AND generation = sqlc.arg(generation)
  AND observed_generation < sqlc.arg(generation)
RETURNING *;

-- name: MarkNodeOperationBlocked :one
UPDATE node_operations
SET status = 'blocked', observed_generation = sqlc.arg(generation),
    progress = sqlc.arg(progress), error_code = sqlc.arg(error_code),
    locked_until = NULL, completed_at = now(), updated_at = now()
WHERE id = sqlc.arg(id) AND generation = sqlc.arg(generation)
  AND observed_generation < sqlc.arg(generation)
RETURNING *;

-- name: MarkNodeOperationRetrying :one
UPDATE node_operations
SET status = 'retrying', progress = sqlc.arg(progress),
    error_code = sqlc.arg(error_code), locked_until = NULL, updated_at = now()
WHERE id = sqlc.arg(id) AND generation = sqlc.arg(generation)
  AND observed_generation < sqlc.arg(generation)
RETURNING *;

-- name: MarkNodeOperationFailed :one
UPDATE node_operations
SET status = 'failed', observed_generation = sqlc.arg(generation),
    progress = sqlc.arg(progress), error_code = sqlc.arg(error_code),
    locked_until = NULL, completed_at = now(), updated_at = now()
WHERE id = sqlc.arg(id) AND generation = sqlc.arg(generation)
  AND observed_generation < sqlc.arg(generation)
RETURNING *;
