CREATE TABLE public.refresh_session_families (
    family_hash bytea PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revoke_reason text NOT NULL DEFAULT '',
    CONSTRAINT refresh_session_family_hash_length CHECK (octet_length(family_hash) = 32),
    CONSTRAINT refresh_session_family_expiry CHECK (expires_at > created_at),
    CONSTRAINT refresh_session_family_revoke_state CHECK ((revoked_at IS NULL AND revoke_reason = '') OR (revoked_at IS NOT NULL AND revoke_reason <> ''))
);

CREATE TABLE public.refresh_session_tokens (
    jti_hash bytea PRIMARY KEY,
    family_hash bytea NOT NULL REFERENCES public.refresh_session_families(family_hash) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    replaced_by_jti_hash bytea,
    CONSTRAINT refresh_session_token_jti_hash_length CHECK (octet_length(jti_hash) = 32),
    CONSTRAINT refresh_session_token_replacement_hash_length CHECK (replaced_by_jti_hash IS NULL OR octet_length(replaced_by_jti_hash) = 32),
    CONSTRAINT refresh_session_token_expiry CHECK (expires_at > created_at),
    CONSTRAINT refresh_session_token_consume_state CHECK ((consumed_at IS NULL AND replaced_by_jti_hash IS NULL) OR (consumed_at IS NOT NULL AND replaced_by_jti_hash IS NOT NULL))
);

CREATE INDEX refresh_session_families_user_active_idx
    ON public.refresh_session_families (user_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE INDEX refresh_session_families_expiry_idx
    ON public.refresh_session_families (expires_at);

CREATE INDEX refresh_session_tokens_family_idx
    ON public.refresh_session_tokens (family_hash, created_at);
