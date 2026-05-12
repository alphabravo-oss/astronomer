DROP TABLE IF EXISTS jwt_revocations;

DROP INDEX IF EXISTS idx_users_locked_until;

ALTER TABLE users
    DROP COLUMN IF EXISTS tokens_invalidated_at,
    DROP COLUMN IF EXISTS locked_reason,
    DROP COLUMN IF EXISTS locked_until,
    DROP COLUMN IF EXISTS failed_login_at,
    DROP COLUMN IF EXISTS failed_login_count;
