DROP INDEX IF EXISTS idx_cluster_agent_tokens_revoked_at;

ALTER TABLE cluster_agent_tokens
    DROP COLUMN IF EXISTS revoked_at;
