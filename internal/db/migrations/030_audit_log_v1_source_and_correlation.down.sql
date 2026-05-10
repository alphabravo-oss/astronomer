DROP INDEX IF EXISTS idx_audit_log_correlation_created;
DROP INDEX IF EXISTS idx_audit_log_source_created;

ALTER TABLE audit_log
    DROP COLUMN IF EXISTS correlation_id,
    DROP COLUMN IF EXISTS source;
