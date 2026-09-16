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
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- name: CountToolOperations :one
SELECT count(*) FROM tool_operations
WHERE (
    sqlc.narg(target_type)::text IS NULL OR target_type = sqlc.narg(target_type)::text
) AND (
    sqlc.narg(target_key)::text IS NULL OR target_key = sqlc.narg(target_key)::text
) AND (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
);

-- name: ListToolOperationsForScopes :many
SELECT * FROM tool_operations
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

-- name: CountToolOperationsForScopes :one
SELECT count(*) FROM tool_operations
WHERE (
    sqlc.narg(target_type)::text IS NULL OR target_type = sqlc.narg(target_type)::text
) AND (
    sqlc.narg(target_key)::text IS NULL OR target_key = sqlc.narg(target_key)::text
) AND (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
) AND payload->>'clusterId' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND (payload->>'clusterId')::uuid = ANY(sqlc.arg(cluster_ids)::uuid[]);

-- name: ListPendingToolOperations :many
SELECT operation.* FROM tool_operations operation
WHERE operation.status = 'running' OR (operation.status = 'pending' AND NOT EXISTS (
    SELECT 1 FROM tool_operations running
    WHERE running.target_type = operation.target_type AND running.target_key = operation.target_key
      AND running.status = 'running'
))
ORDER BY operation.created_at ASC
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
WHERE tool_operations.id = $1
  AND (
      tool_operations.status = 'pending'
      OR (tool_operations.status = 'running' AND (tool_operations.started_at IS NULL OR tool_operations.started_at < now() - interval '1 minute'))
  )
  AND NOT EXISTS (
      SELECT 1 FROM tool_operations other
      WHERE other.target_type = tool_operations.target_type
        AND other.target_key = tool_operations.target_key
        AND other.status = 'running' AND other.id <> tool_operations.id
  )
RETURNING *;

-- name: RenewToolOperationLease :one
UPDATE tool_operations
SET started_at = now(), updated_at = now()
WHERE id = $1 AND status = 'running' AND attempt_count = $2
RETURNING *;

-- name: CheckpointToolOperation :one
WITH checkpoint AS (
    UPDATE tool_operations
    SET payload = sqlc.arg(payload)::jsonb, updated_at = now(), started_at = now()
    WHERE id = sqlc.arg(id)::uuid
      AND status = 'running' AND attempt_count = sqlc.arg(attempt_count)::integer
    RETURNING *
), event AS (
    INSERT INTO tool_operation_events (operation_id, level, stage, message, detail)
    SELECT id, sqlc.arg(event_level)::text, sqlc.arg(event_stage)::text,
           sqlc.arg(event_message)::text, sqlc.arg(event_detail)::jsonb
    FROM checkpoint
)
SELECT * FROM checkpoint;

-- name: FinishToolOperation :one
UPDATE tool_operations
SET status = sqlc.arg(final_status)::text, error_message = sqlc.arg(error_message)::text,
    completed_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND status = 'running'
  AND attempt_count = sqlc.arg(attempt_count)::integer
RETURNING *;

-- name: MarkToolOperationSuperseded :one
UPDATE tool_operations
SET
    status = 'superseded',
    completed_at = now(),
    error_message = $2,
    updated_at = now()
WHERE id = $1 AND status = 'pending'
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
  AND status IN ('failed', 'superseded')
RETURNING *;
