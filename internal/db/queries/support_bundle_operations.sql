-- name: CreateSupportBundleOperation :one
WITH pruned AS (
    DELETE FROM support_bundle_operations
    WHERE expires_at <= now()
), operation AS (
    INSERT INTO support_bundle_operations (
        requested_by, idempotency_scope, idempotency_key, request_digest
    ) VALUES (
        sqlc.arg(requested_by), sqlc.arg(idempotency_scope),
        sqlc.arg(idempotency_key), sqlc.arg(request_digest)
    )
    ON CONFLICT (idempotency_scope, idempotency_key) DO UPDATE
    SET idempotency_key = support_bundle_operations.idempotency_key
    WHERE support_bundle_operations.request_digest = EXCLUDED.request_digest
      AND support_bundle_operations.requested_by = EXCLUDED.requested_by
    RETURNING support_bundle_operations.*, (xmax = 0) AS created
), queued AS (
    INSERT INTO task_outbox (
        dedupe_key, task_type, payload, queue_name, max_retry,
        timeout_seconds, unique_seconds, max_delivery_attempts, next_attempt_at
    )
    SELECT 'support-bundle:' || id::text, 'support_bundle:generate',
           convert_to(jsonb_build_object('operation_id', id::text)::text, 'UTF8'),
           'tunnel', 4, 600, 900, 20, now()
    FROM operation
    WHERE created
    ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING
)
SELECT operation.* FROM operation;

-- name: GetSupportBundleOperation :one
SELECT id, requested_by, status, attempt_count, error_code, filename,
       artifact_content_type, artifact_sha256, artifact_size, expires_at,
       completed_at, created_at, updated_at
FROM support_bundle_operations
WHERE id = sqlc.arg(id);

-- name: GetSupportBundleArtifact :one
SELECT id, filename, artifact_content_type, artifact, artifact_sha256,
       artifact_size, expires_at
FROM support_bundle_operations
WHERE id = sqlc.arg(id) AND status = 'succeeded';

-- name: ClaimSupportBundleOperation :one
UPDATE support_bundle_operations
SET status = 'running', attempt_count = attempt_count + 1,
    locked_until = sqlc.arg(locked_until), error_code = '', updated_at = now()
WHERE id = sqlc.arg(id)
  AND expires_at > now()
  AND (
      status IN ('pending', 'retrying')
      OR (status = 'running' AND (locked_until IS NULL OR locked_until <= now()))
  )
RETURNING *;

-- name: MarkSupportBundleOperationSucceeded :one
UPDATE support_bundle_operations
SET status = 'succeeded', locked_until = NULL, error_code = '',
    filename = sqlc.arg(filename), artifact_content_type = 'application/zip',
    artifact = sqlc.arg(artifact), artifact_sha256 = sqlc.arg(artifact_sha256),
    artifact_size = octet_length(sqlc.arg(artifact)),
    completed_at = COALESCE(completed_at, now()), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'running' AND expires_at > now()
RETURNING *;

-- name: MarkSupportBundleOperationRetrying :one
UPDATE support_bundle_operations
SET status = 'retrying', locked_until = NULL,
    error_code = left(sqlc.arg(error_code), 128), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'running'
RETURNING *;

-- name: MarkSupportBundleOperationFailed :one
UPDATE support_bundle_operations
SET status = 'failed', locked_until = NULL,
    error_code = left(sqlc.arg(error_code), 128),
    completed_at = COALESCE(completed_at, now()), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'running'
RETURNING *;

-- name: RecoverSupportBundleOperationOutbox :execrows
UPDATE task_outbox outbox
SET status = 'pending', attempt_count = 0, next_attempt_at = now(),
    locked_until = NULL, delivered_at = NULL, last_error = '', updated_at = now()
FROM support_bundle_operations operation
WHERE outbox.dedupe_key = 'support-bundle:' || operation.id::text
  AND outbox.status = 'delivered'
  AND operation.expires_at > now()
  AND (
      (operation.status IN ('pending', 'retrying') AND operation.updated_at <= sqlc.arg(stale_before))
      OR (operation.status = 'running' AND operation.locked_until <= now())
  );
