DROP INDEX IF EXISTS idx_cluster_registration_tokens_token_hash;
CREATE INDEX IF NOT EXISTS idx_cluster_registration_tokens_token_hash
    ON cluster_registration_tokens (token_hash)
    WHERE token_hash <> '';
ALTER TABLE cluster_agent_tokens DROP COLUMN IF EXISTS adopted_at;
