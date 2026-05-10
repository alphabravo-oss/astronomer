-- Backfill pre-cutover audit rows into the partitioned audit_log table when
-- upgrading an older database that still has audit_logs.
--
-- Rows copied from the old table are tagged actor_auth_method=legacy_backfill
-- so this migration remains reversible and operators can distinguish imported
-- history from native v1 writes if needed.

DO $$
DECLARE
    month_start TIMESTAMPTZ;
BEGIN
    IF to_regclass('public.audit_logs') IS NULL THEN
        RETURN;
    END IF;

    FOR month_start IN
        SELECT DISTINCT date_trunc('month', created_at)::timestamptz
        FROM audit_logs
    LOOP
        PERFORM create_audit_log_partition(month_start);
    END LOOP;

    INSERT INTO audit_log (
        id,
        created_at,
        schema_version,
        user_id,
        actor_auth_method,
        action,
        resource_type,
        resource_id,
        resource_name,
        http_method,
        path,
        status_code,
        duration_ms,
        request_id,
        ip_address,
        user_agent,
        detail
    )
    SELECT
        id,
        created_at,
        'audit-v1',
        user_id,
        'legacy_backfill',
        action,
        resource_type,
        resource_id,
        resource_name,
        COALESCE(detail->>'method', ''),
        COALESCE(detail->>'path', ''),
        CASE
            WHEN detail ? 'status_code' AND (detail->>'status_code') ~ '^-?[0-9]+$' THEN (detail->>'status_code')::integer
            WHEN detail ? 'status' AND (detail->>'status') ~ '^-?[0-9]+$' THEN (detail->>'status')::integer
            ELSE 0
        END,
        CASE
            WHEN detail ? 'duration_ms' AND (detail->>'duration_ms') ~ '^-?[0-9]+$' THEN (detail->>'duration_ms')::bigint
            ELSE 0
        END,
        request_id,
        ip_address,
        user_agent,
        detail
    FROM audit_logs
    ON CONFLICT (id, created_at) DO NOTHING;
END;
$$;
