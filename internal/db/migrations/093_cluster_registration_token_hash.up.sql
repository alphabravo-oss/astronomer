ALTER TABLE cluster_registration_tokens
    ADD COLUMN IF NOT EXISTS token_hash VARCHAR(64) NOT NULL DEFAULT '';

UPDATE cluster_registration_tokens
SET token_hash = encode(digest(token, 'sha256'), 'hex')
WHERE token_hash = ''
  AND token <> '';

CREATE INDEX IF NOT EXISTS idx_cluster_registration_tokens_token_hash
    ON cluster_registration_tokens (token_hash)
    WHERE token_hash <> '';
