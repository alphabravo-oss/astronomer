-- name: CreateResourceOperationIdempotent :one
INSERT INTO resource_operations (
    idempotency_scope, idempotency_key, request_digest, cluster_id,
    resource_type, namespace, resource_name, action, api_path,
    required_verb, manifest_encrypted, force_apply, created_by_id
) VALUES (
    sqlc.arg(idempotency_scope), sqlc.arg(idempotency_key), sqlc.arg(request_digest),
    sqlc.arg(cluster_id), sqlc.arg(resource_type), sqlc.arg(namespace),
    sqlc.arg(resource_name), sqlc.arg(action), sqlc.arg(api_path),
    sqlc.arg(required_verb), sqlc.arg(manifest_encrypted), sqlc.arg(force_apply), sqlc.narg(created_by_id)
)
ON CONFLICT (idempotency_scope, idempotency_key)
DO UPDATE SET updated_at = resource_operations.updated_at
RETURNING *;

-- name: GetResourceOperation :one
SELECT * FROM resource_operations WHERE id = sqlc.arg(id);

-- name: ClaimResourceOperationGeneration :one
UPDATE resource_operations
SET status = 'running',
    attempt_count = attempt_count + 1,
    locked_until = sqlc.arg(locked_until),
    error_code = '',
    completed_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND generation = sqlc.arg(generation)
  AND observed_generation < sqlc.arg(generation)
  AND (
      status IN ('pending', 'retrying')
      OR (status = 'running' AND (locked_until IS NULL OR locked_until <= sqlc.arg(now)))
  )
RETURNING *;

-- name: MarkResourceOperationRetrying :one
UPDATE resource_operations
SET status = 'retrying',
    locked_until = NULL,
    error_code = left(sqlc.arg(error_code), 128),
    observed_status_code = sqlc.narg(observed_status_code),
    completed_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND generation = sqlc.arg(generation)
  AND observed_generation < sqlc.arg(generation)
RETURNING *;

-- name: MarkResourceOperationSucceeded :one
UPDATE resource_operations
SET status = 'succeeded',
    observed_generation = sqlc.arg(generation),
    locked_until = NULL,
    error_code = '',
    observed_status_code = sqlc.arg(observed_status_code),
    observed_resource_version = sqlc.arg(observed_resource_version),
    completed_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND generation = sqlc.arg(generation)
  AND observed_generation < sqlc.arg(generation)
RETURNING *;

-- name: MarkResourceOperationFailed :one
UPDATE resource_operations
SET status = 'failed',
    locked_until = NULL,
    error_code = left(sqlc.arg(error_code), 128),
    observed_status_code = sqlc.narg(observed_status_code),
    completed_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND generation = sqlc.arg(generation)
  AND observed_generation < sqlc.arg(generation)
RETURNING *;
