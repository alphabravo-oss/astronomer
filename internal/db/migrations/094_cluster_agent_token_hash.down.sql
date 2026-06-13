DROP INDEX IF EXISTS idx_cluster_agent_tokens_token_hash;

UPDATE cluster_agent_tokens
SET token = token_hash
WHERE token = '' AND token_hash <> '';

ALTER TABLE cluster_agent_tokens
    DROP COLUMN IF EXISTS token_hash;

CREATE UNIQUE INDEX IF NOT EXISTS idx_cluster_agent_tokens_token
    ON cluster_agent_tokens (token);
