ALTER TABLE cluster_agent_tokens
    ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_cluster_agent_tokens_revoked_at
    ON cluster_agent_tokens (revoked_at);
