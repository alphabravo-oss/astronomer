-- Durable audit intent lifecycle. These rows contain only the sanitized,
-- bounded audit-v1 envelope; request bodies and credentials are prohibited.

-- name: UpsertAuditOutbox :one
INSERT INTO audit_outbox (
    id, dedupe_key, event_created_at, schema_version, user_id,
    actor_auth_method, action, resource_type, resource_id, resource_name,
    http_method, path, status_code, duration_ms, request_id, ip_address,
    user_agent, detail, source, correlation_id, action_class, max_attempts
) VALUES (
    sqlc.arg(id), sqlc.arg(dedupe_key), sqlc.arg(event_created_at),
    sqlc.arg(schema_version), sqlc.narg(user_id), sqlc.arg(actor_auth_method),
    sqlc.arg(action), sqlc.arg(resource_type), sqlc.arg(resource_id),
    sqlc.arg(resource_name), sqlc.arg(http_method), sqlc.arg(path),
    sqlc.arg(status_code), sqlc.arg(duration_ms), sqlc.arg(request_id),
    sqlc.narg(ip_address), sqlc.arg(user_agent), sqlc.arg(detail),
    sqlc.arg(source), sqlc.arg(correlation_id), sqlc.arg(action_class),
    sqlc.arg(max_attempts)
)
ON CONFLICT (dedupe_key) DO UPDATE
SET dedupe_key = EXCLUDED.dedupe_key
RETURNING *;

-- name: ClaimDueAuditOutbox :many
WITH picked AS (
    SELECT id
    FROM audit_outbox
    WHERE status IN ('pending', 'failed', 'delivering')
      AND next_attempt_at <= sqlc.arg(now)
      AND (locked_until IS NULL OR locked_until <= sqlc.arg(now))
    ORDER BY next_attempt_at ASC, created_at ASC, id ASC
    LIMIT sqlc.arg(batch_limit)
    FOR UPDATE SKIP LOCKED
)
UPDATE audit_outbox AS o
SET status = 'delivering',
    locked_until = sqlc.arg(locked_until),
    attempt_count = attempt_count + 1,
    updated_at = sqlc.arg(now)
FROM picked
WHERE o.id = picked.id
RETURNING o.*;

-- name: DeliverAuditOutbox :one
WITH source AS (
    SELECT ao.*
    FROM audit_outbox AS ao
    WHERE ao.id = sqlc.arg(outbox_id)
      AND ao.status = 'delivering'
), persisted AS (
    INSERT INTO audit_log (
        id, created_at, schema_version, user_id, actor_auth_method, action,
        resource_type, resource_id, resource_name, http_method, path,
        status_code, duration_ms, request_id, ip_address, user_agent, detail,
        source, correlation_id, action_class
    )
    SELECT
        id, event_created_at, schema_version, user_id, actor_auth_method,
        action, resource_type, resource_id, resource_name, http_method, path,
        status_code, duration_ms, request_id, ip_address, user_agent, detail,
        source, correlation_id, action_class
    FROM source
    ON CONFLICT (id, created_at) DO NOTHING
), siem_fanout AS (
    -- External audit delivery must be as durable as the canonical audit row.
    -- Select every currently enabled, matching forwarder and create its queue
    -- receipt before the outbox is acknowledged. A retry uses the stable audit
    -- UUID dedupe key, so a crash/replay cannot create duplicate queue rows.
    INSERT INTO siem_forward_queue (
        forwarder_id, event_name, payload, severity, dedupe_key
    )
    SELECT
        forwarder.id,
        'audit.' || source.action,
        jsonb_build_object(
            'event_name', 'audit.' || source.action,
            'event_id', source.id::text,
            'timestamp', source.event_created_at,
            'detail', jsonb_build_object(
                'action', source.action,
                'resource_type', source.resource_type,
                'resource_id', source.resource_id,
                'resource_name', source.resource_name,
                'actor_user_id', coalesce(source.user_id::text, ''),
                'actor_auth_method', source.actor_auth_method,
                'correlation_id', source.correlation_id,
                'request_id', source.request_id,
                'source', source.source,
                'http_method', source.http_method,
                'path', source.path,
                'status_code', source.status_code,
                'detail', source.detail
            )
        ),
        CASE
            WHEN source.action LIKE '%.failed%' OR source.action LIKE '%.error%' OR source.action LIKE '%.rejected%' THEN 'err'
            WHEN source.action LIKE '%.deleted%' OR source.action LIKE '%.disconnected%' OR source.action LIKE '%.disabled%' THEN 'warn'
            ELSE 'notice'
        END,
        'audit:' || source.id::text
    FROM source
    JOIN siem_forwarders AS forwarder
      ON forwarder.enabled
     AND (
        forwarder.event_filters = '[]'::jsonb
        OR EXISTS (
            SELECT 1
            FROM jsonb_array_elements_text(
                CASE
                    WHEN jsonb_typeof(forwarder.event_filters) = 'array' THEN forwarder.event_filters
                    ELSE '[]'::jsonb
                END
            ) AS configured_filter(value)
            CROSS JOIN LATERAL regexp_split_to_table(configured_filter.value, ',') AS alternative(value)
            WHERE btrim(alternative.value) = ''
               OR public.astronomer_event_glob_match(
                    btrim(alternative.value),
                    'audit.' || source.action
               )
        )
     )
    ON CONFLICT (forwarder_id, dedupe_key) WHERE dedupe_key IS NOT NULL
    DO UPDATE SET dedupe_key = EXCLUDED.dedupe_key
    RETURNING id
), marked AS (
    UPDATE audit_outbox AS o
    SET status = 'delivered',
        delivered_at = sqlc.arg(delivered_at),
        locked_until = NULL,
        last_error = '',
        updated_at = sqlc.arg(delivered_at)
    WHERE o.id = sqlc.arg(outbox_id)
      AND EXISTS (SELECT 1 FROM source)
    RETURNING o.*
)
SELECT * FROM marked;

-- name: MarkAuditOutboxFailed :one
UPDATE audit_outbox
SET status = CASE WHEN attempt_count >= max_attempts THEN 'dead' ELSE 'failed' END,
    next_attempt_at = sqlc.arg(next_attempt_at),
    locked_until = NULL,
    last_error = left(sqlc.arg(last_error), 2048),
    updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(outbox_id)
  AND status = 'delivering'
RETURNING *;

-- name: ResetExpiredAuditOutboxLeases :execrows
UPDATE audit_outbox
SET status = 'failed',
    locked_until = NULL,
    next_attempt_at = sqlc.arg(now),
    last_error = CASE WHEN last_error = '' THEN 'delivery lease expired' ELSE last_error END,
    updated_at = sqlc.arg(now)
WHERE status = 'delivering'
  AND locked_until <= sqlc.arg(now);

-- name: GetAuditOutboxHealth :one
SELECT
    count(*) FILTER (WHERE status IN ('pending', 'failed', 'delivering'))::bigint AS pending_count,
    count(*) FILTER (WHERE status = 'dead')::bigint AS dead_count,
    count(*) FILTER (WHERE status = 'delivered')::bigint AS delivered_count,
    coalesce(
        min(created_at) FILTER (WHERE status IN ('pending', 'failed', 'delivering')),
        'epoch'::timestamptz
    ) AS oldest_pending_at,
    coalesce(
        max(delivered_at) FILTER (WHERE status = 'delivered'),
        'epoch'::timestamptz
    ) AS last_delivered_at
FROM audit_outbox;

-- name: DeleteDeliveredAuditOutboxBefore :execrows
DELETE FROM audit_outbox
WHERE status = 'delivered'
  AND delivered_at < sqlc.arg(cutoff);
