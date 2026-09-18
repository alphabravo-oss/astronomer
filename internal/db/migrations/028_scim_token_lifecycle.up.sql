ALTER TABLE public.scim_tokens
    ADD COLUMN expires_at timestamptz NOT NULL DEFAULT (now() + interval '30 days'),
    ADD COLUMN revoked_at timestamptz;

CREATE INDEX idx_scim_tokens_active_hash
    ON public.scim_tokens (token_hash)
    WHERE revoked_at IS NULL;
