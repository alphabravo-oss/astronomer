-- name: CreateLoggingOperation :one
INSERT INTO logging_operations (
    target_type,
    target_key,
    operation_type,
    payload,
    status,
    created_by_id
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetLoggingOperation :one
SELECT * FROM logging_operations WHERE id = $1;

-- name: ListLoggingOperations :many
SELECT * FROM logging_operations
WHERE (
    sqlc.narg(target_type)::text IS NULL OR target_type = sqlc.narg(target_type)::text
) AND (
    sqlc.narg(target_key)::text IS NULL OR target_key = sqlc.narg(target_key)::text
) AND (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
)
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- name: CountLoggingOperations :one
SELECT count(*) FROM logging_operations
WHERE (
    sqlc.narg(target_type)::text IS NULL OR target_type = sqlc.narg(target_type)::text
) AND (
    sqlc.narg(target_key)::text IS NULL OR target_key = sqlc.narg(target_key)::text
) AND (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
);

-- name: ListLoggingOperationsForScopes :many
SELECT operations.*
FROM logging_operations operations
LEFT JOIN logging_outputs output ON
    operations.target_type = 'output'
    AND output.id = CASE
        WHEN operations.target_key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN operations.target_key::uuid
    END
LEFT JOIN logging_pipelines pipeline ON
    operations.target_type = 'pipeline'
    AND pipeline.id = CASE
        WHEN operations.target_key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN operations.target_key::uuid
    END
WHERE (
    sqlc.narg(target_type)::text IS NULL OR operations.target_type = sqlc.narg(target_type)::text
) AND (
    sqlc.narg(target_key)::text IS NULL OR operations.target_key = sqlc.narg(target_key)::text
) AND (
    sqlc.narg(status)::text IS NULL OR operations.status = sqlc.narg(status)::text
) AND COALESCE(
    CASE
        WHEN operations.payload->>'cluster_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN (operations.payload->>'cluster_id')::uuid
    END,
    output.cluster_id,
    pipeline.cluster_id
) = ANY(sqlc.arg(cluster_ids)::uuid[])
ORDER BY operations.created_at DESC, operations.id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountLoggingOperationsForScopes :one
SELECT count(*)
FROM logging_operations operations
LEFT JOIN logging_outputs output ON
    operations.target_type = 'output'
    AND output.id = CASE
        WHEN operations.target_key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN operations.target_key::uuid
    END
LEFT JOIN logging_pipelines pipeline ON
    operations.target_type = 'pipeline'
    AND pipeline.id = CASE
        WHEN operations.target_key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN operations.target_key::uuid
    END
WHERE (
    sqlc.narg(target_type)::text IS NULL OR operations.target_type = sqlc.narg(target_type)::text
) AND (
    sqlc.narg(target_key)::text IS NULL OR operations.target_key = sqlc.narg(target_key)::text
) AND (
    sqlc.narg(status)::text IS NULL OR operations.status = sqlc.narg(status)::text
) AND COALESCE(
    CASE
        WHEN operations.payload->>'cluster_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN (operations.payload->>'cluster_id')::uuid
    END,
    output.cluster_id,
    pipeline.cluster_id
) = ANY(sqlc.arg(cluster_ids)::uuid[]);

-- name: ListPendingLoggingOperations :many
SELECT * FROM logging_operations
WHERE status IN ('pending', 'running')
ORDER BY created_at ASC
LIMIT $1;

-- name: MarkLoggingOperationRunning :one
-- Atomic claim (CORR-R01): pending or stale running only — see tool_operations.
UPDATE logging_operations
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

-- name: MarkLoggingOperationCompleted :one
UPDATE logging_operations
SET
    status = 'completed',
    completed_at = now(),
    error_message = '',
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkLoggingOperationFailed :one
UPDATE logging_operations
SET
    status = 'failed',
    completed_at = now(),
    error_message = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkLoggingOperationSuperseded :one
UPDATE logging_operations
SET
    status = 'superseded',
    completed_at = now(),
    error_message = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RequeueLoggingOperation :one
UPDATE logging_operations
SET
    status = 'pending',
    started_at = NULL,
    completed_at = NULL,
    error_message = '',
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateLoggingOperationEvent :one
INSERT INTO logging_operation_events (
    operation_id,
    level,
    stage,
    message,
    detail
)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListLoggingOperationEvents :many
SELECT * FROM logging_operation_events
WHERE operation_id = $1
ORDER BY created_at ASC;
