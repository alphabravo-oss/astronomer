ALTER TABLE audit_log
    ADD COLUMN IF NOT EXISTS source VARCHAR(16) NOT NULL DEFAULT 'service',
    ADD COLUMN IF NOT EXISTS correlation_id VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_audit_log_source_created
    ON audit_log (source, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_log_correlation_created
    ON audit_log (correlation_id, created_at DESC);
