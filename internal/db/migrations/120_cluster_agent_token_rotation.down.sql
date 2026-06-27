DROP INDEX IF EXISTS idx_cluster_agent_tokens_last_rotated_at;
DROP INDEX IF EXISTS idx_cluster_agent_tokens_previous_token_hash;

ALTER TABLE cluster_agent_tokens
    DROP COLUMN IF EXISTS last_rotated_at,
    DROP COLUMN IF EXISTS rotation_pending_at,
    DROP COLUMN IF EXISTS previous_token_hash;
