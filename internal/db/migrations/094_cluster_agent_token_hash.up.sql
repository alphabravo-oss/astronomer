ALTER TABLE cluster_agent_tokens
    ADD COLUMN IF NOT EXISTS token_hash VARCHAR(128) NOT NULL DEFAULT '';

UPDATE cluster_agent_tokens
SET token_hash = encode(digest(token, 'sha256'), 'hex')
WHERE token_hash = '' AND token <> '';

ALTER TABLE cluster_agent_tokens
    DROP CONSTRAINT IF EXISTS cluster_agent_tokens_token_key;

DROP INDEX IF EXISTS idx_cluster_agent_tokens_token;

CREATE UNIQUE INDEX IF NOT EXISTS idx_cluster_agent_tokens_token_hash
    ON cluster_agent_tokens (token_hash)
    WHERE token_hash <> '';
