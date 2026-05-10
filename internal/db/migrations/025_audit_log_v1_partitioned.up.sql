-- Create the partitioned audit_log table for upgrade paths that predate the
-- audit schema in 001_initial. Fresh installs already have this schema, so
-- this migration must remain idempotent.

CREATE TABLE IF NOT EXISTS audit_log (
    id                  UUID NOT NULL DEFAULT gen_random_uuid(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    schema_version      VARCHAR(32) NOT NULL DEFAULT 'audit-v1',
    user_id             UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_auth_method   VARCHAR(32) NOT NULL DEFAULT '',
    action              VARCHAR(64) NOT NULL,
    resource_type       VARCHAR(64) NOT NULL,
    resource_id         VARCHAR(255) NOT NULL DEFAULT '',
    resource_name       VARCHAR(255) NOT NULL DEFAULT '',
    http_method         VARCHAR(16) NOT NULL DEFAULT '',
    path                TEXT NOT NULL DEFAULT '',
    status_code         INTEGER NOT NULL DEFAULT 0,
    duration_ms         BIGINT NOT NULL DEFAULT 0,
    request_id          VARCHAR(64) NOT NULL DEFAULT '',
    ip_address          INET,
    user_agent          TEXT NOT NULL DEFAULT '',
    detail              JSONB NOT NULL DEFAULT '{}',
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE INDEX IF NOT EXISTS idx_audit_log_action_created ON audit_log (action, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_resource ON audit_log (resource_type, resource_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_user_created ON audit_log (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_request_id ON audit_log (request_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_schema_created ON audit_log (schema_version, created_at DESC);

CREATE TABLE IF NOT EXISTS audit_log_default PARTITION OF audit_log DEFAULT;

CREATE OR REPLACE FUNCTION create_audit_log_partition(target_month TIMESTAMPTZ)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
    month_start TIMESTAMPTZ := date_trunc('month', target_month);
    month_end   TIMESTAMPTZ := month_start + INTERVAL '1 month';
    partition_name TEXT := 'audit_log_' || to_char(month_start, 'YYYY_MM');
BEGIN
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF audit_log FOR VALUES FROM (%L) TO (%L)',
        partition_name,
        month_start,
        month_end
    );
END;
$$;

SELECT create_audit_log_partition(now());
SELECT create_audit_log_partition(now() + INTERVAL '1 month');
