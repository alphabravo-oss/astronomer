-- Rollback for 044_api_token_hardening.up.sql.

DROP INDEX IF EXISTS idx_api_tokens_user_active;

ALTER TABLE api_tokens
    DROP COLUMN IF EXISTS last_seen_remote_ip,
    DROP COLUMN IF EXISTS allowed_cidrs;
