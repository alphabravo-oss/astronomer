-- Legacy audit rows are backfilled into audit_log by migration 026.
-- Remove the old audit_logs table so the runtime schema has a single audit
-- source of truth.

DROP TABLE IF EXISTS audit_logs CASCADE;
