-- Migration 093 moved registration-token validation to token_hash so freshly
-- minted tokens do not need to be stored in plaintext. The original schema
-- still had a UNIQUE constraint on token, and hash-only rows intentionally use
-- token='', so the second token mint fails with SQLSTATE 23505.
ALTER TABLE cluster_registration_tokens
    DROP CONSTRAINT IF EXISTS cluster_registration_tokens_token_key;

DROP INDEX IF EXISTS cluster_registration_tokens_token_key;

-- Keep legacy plaintext-token lookups indexed without requiring token to be
-- globally unique for hash-only rows.
CREATE INDEX IF NOT EXISTS idx_cluster_registration_tokens_token_nonempty
    ON cluster_registration_tokens (token)
    WHERE token <> '';
