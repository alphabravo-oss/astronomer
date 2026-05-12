-- Reverse migration 063.

DROP TABLE IF EXISTS read_audit_policies;

DROP INDEX IF EXISTS idx_audit_log_class;

ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_action_class_valid;

ALTER TABLE audit_log DROP COLUMN IF EXISTS action_class;
