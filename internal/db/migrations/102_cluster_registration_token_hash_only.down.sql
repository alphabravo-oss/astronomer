DROP INDEX IF EXISTS idx_cluster_registration_tokens_token_nonempty;

-- Best-effort rollback guard: if hash-only rows exist, recreating the old
-- all-token unique constraint would fail because those rows intentionally
-- store token=''. Preserve uniqueness only for legacy plaintext tokens.
CREATE UNIQUE INDEX IF NOT EXISTS idx_cluster_registration_tokens_token_nonempty
    ON cluster_registration_tokens (token)
    WHERE token <> '';
