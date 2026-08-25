-- name: CreateCatalogOperation :one
INSERT INTO catalog_operations (
    target_type,
    target_key,
    operation_type,
    payload,
    status,
    created_by_id
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCatalogOperation :one
SELECT * FROM catalog_operations WHERE id = $1;

-- name: ListCatalogOperations :many
SELECT * FROM catalog_operations
WHERE (
    sqlc.narg(target_type)::text IS NULL OR target_type = sqlc.narg(target_type)::text
) AND (
    sqlc.narg(target_key)::text IS NULL OR target_key = sqlc.narg(target_key)::text
) AND (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
)
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- name: CountCatalogOperations :one
SELECT count(*) FROM catalog_operations
WHERE (
    sqlc.narg(target_type)::text IS NULL OR target_type = sqlc.narg(target_type)::text
) AND (
    sqlc.narg(target_key)::text IS NULL OR target_key = sqlc.narg(target_key)::text
) AND (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
);

-- name: ListCatalogOperationsForScopes :many
SELECT * FROM catalog_operations
WHERE (
    sqlc.narg(target_type)::text IS NULL OR target_type = sqlc.narg(target_type)::text
) AND (
    sqlc.narg(target_key)::text IS NULL OR target_key = sqlc.narg(target_key)::text
) AND (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
) AND payload->>'clusterId' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND (payload->>'clusterId')::uuid = ANY(sqlc.arg(cluster_ids)::uuid[])
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountCatalogOperationsForScopes :one
SELECT count(*) FROM catalog_operations
WHERE (
    sqlc.narg(target_type)::text IS NULL OR target_type = sqlc.narg(target_type)::text
) AND (
    sqlc.narg(target_key)::text IS NULL OR target_key = sqlc.narg(target_key)::text
) AND (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
) AND payload->>'clusterId' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND (payload->>'clusterId')::uuid = ANY(sqlc.arg(cluster_ids)::uuid[]);

-- name: ListPendingCatalogOperations :many
SELECT * FROM catalog_operations
WHERE status IN ('pending', 'running')
ORDER BY created_at ASC
LIMIT $1;

-- name: MarkCatalogOperationRunning :one
-- Atomic claim (CORR-R01): pending or stale running only — see tool_operations.
UPDATE catalog_operations
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

-- name: MarkCatalogOperationCompleted :one
UPDATE catalog_operations
SET
    status = 'completed',
    completed_at = now(),
    error_message = '',
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkCatalogOperationFailed :one
UPDATE catalog_operations
SET
    status = 'failed',
    completed_at = now(),
    error_message = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkCatalogOperationSuperseded :one
UPDATE catalog_operations
SET
    status = 'superseded',
    completed_at = now(),
    error_message = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RequeueCatalogOperation :one
UPDATE catalog_operations
SET
    status = 'pending',
    started_at = NULL,
    completed_at = NULL,
    error_message = '',
    updated_at = now()
WHERE id = $1
RETURNING *;
