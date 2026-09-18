DROP TABLE IF EXISTS public.gitops_webhook_receipts;

ALTER TABLE public.gitops_registration_sources
    DROP CONSTRAINT IF EXISTS gitops_registration_sources_webhook_provider_valid,
    DROP COLUMN IF EXISTS webhook_secret_encrypted,
    DROP COLUMN IF EXISTS webhook_provider;
