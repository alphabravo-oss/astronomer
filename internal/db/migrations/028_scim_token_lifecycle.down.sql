DROP INDEX IF EXISTS public.idx_scim_tokens_active_hash;

ALTER TABLE public.scim_tokens
    DROP COLUMN IF EXISTS revoked_at,
    DROP COLUMN IF EXISTS expires_at;
