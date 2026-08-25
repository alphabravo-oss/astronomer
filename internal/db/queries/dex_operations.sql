-- name: ReserveDexOperation :one
INSERT INTO dex_operations (
    id, action, target_id, idempotency_scope, idempotency_key,
    request_digest, payload_encrypted, created_by
) VALUES (
    sqlc.arg(id), sqlc.arg(action), sqlc.arg(target_id), sqlc.arg(idempotency_scope),
    sqlc.arg(idempotency_key), sqlc.arg(request_digest), sqlc.arg(payload_encrypted),
    sqlc.arg(created_by)
)
ON CONFLICT (idempotency_scope, idempotency_key) DO UPDATE
SET idempotency_key = dex_operations.idempotency_key
WHERE dex_operations.id = EXCLUDED.id
  AND dex_operations.action = EXCLUDED.action
  AND dex_operations.target_id = EXCLUDED.target_id
  AND dex_operations.request_digest = EXCLUDED.request_digest
RETURNING dex_operations.*, (xmax = 0) AS created;

-- name: QueueDexOperation :one
WITH operation AS (
    UPDATE dex_operations
    SET runtime_generation = sqlc.arg(runtime_generation), updated_at = now()
    WHERE dex_operations.id = sqlc.arg(id) AND dex_operations.status = 'pending'
    RETURNING *
), queued AS (
    INSERT INTO task_outbox (
        dedupe_key, task_type, payload, queue_name, max_retry,
        timeout_seconds, unique_seconds, max_delivery_attempts, next_attempt_at
    )
    SELECT 'dex-operation:' || id::text, 'dex:apply_operation',
           convert_to(jsonb_build_object('operation_id', id::text)::text, 'UTF8'),
           'tunnel', 8, 120, 1800, 20, now()
    FROM operation
    ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING
    RETURNING id
)
SELECT operation.* FROM operation CROSS JOIN queued;

-- name: GetDexOperation :one
SELECT * FROM dex_operations WHERE id = sqlc.arg(id);

-- name: ClaimDexOperation :one
UPDATE dex_operations
SET status = 'running', phase = 'reconciling', attempt_count = attempt_count + 1,
    locked_until = sqlc.arg(locked_until), error_code = '', updated_at = now()
WHERE id = sqlc.arg(id)
  AND (status IN ('pending', 'retrying') OR (status = 'running' AND locked_until <= now()))
RETURNING *;

-- name: SetDexOperationPhase :exec
UPDATE dex_operations SET phase = sqlc.arg(phase), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'running';

-- name: MarkDexOperationSucceeded :exec
UPDATE dex_operations
SET status = 'succeeded', phase = 'completed', locked_until = NULL,
    error_code = '', completed_at = COALESCE(completed_at, now()), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'running';

-- name: MarkDexOperationRetrying :exec
UPDATE dex_operations
SET status = 'retrying', locked_until = NULL, error_code = sqlc.arg(error_code), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'running';

-- name: MarkDexOperationFailed :exec
UPDATE dex_operations
SET status = 'failed', phase = 'completed', locked_until = NULL,
    error_code = sqlc.arg(error_code), completed_at = COALESCE(completed_at, now()), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'running';

-- name: RecoverDexOperationOutbox :execrows
UPDATE task_outbox outbox
SET status = 'pending', attempt_count = 0, next_attempt_at = now(),
    locked_until = NULL, delivered_at = NULL, last_error = '', updated_at = now()
FROM dex_operations operation
WHERE outbox.dedupe_key = 'dex-operation:' || operation.id::text
  AND outbox.status = 'delivered'
  AND (
      (operation.status IN ('pending', 'retrying') AND operation.updated_at <= sqlc.arg(stale_before))
      OR (operation.status = 'running' AND operation.locked_until <= now())
  );
