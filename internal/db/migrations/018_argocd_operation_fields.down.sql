DROP INDEX IF EXISTS idx_argocd_operations_running_poll;

ALTER TABLE argocd_operations
    DROP COLUMN IF EXISTS revision,
    DROP COLUMN IF EXISTS message,
    DROP COLUMN IF EXISTS operation_id,
    DROP COLUMN IF EXISTS phase,
    DROP COLUMN IF EXISTS poll_attempts,
    DROP COLUMN IF EXISTS last_polled_at;
