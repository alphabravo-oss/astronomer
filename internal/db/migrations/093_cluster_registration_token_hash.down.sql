DROP INDEX IF EXISTS idx_cluster_registration_tokens_token_hash;

ALTER TABLE cluster_registration_tokens
    DROP COLUMN IF EXISTS token_hash;
