-- Per-source inbound GitOps webhook authentication and replay protection.
-- Secrets are Fernet ciphertexts; plaintext never lands in Postgres.
ALTER TABLE public.gitops_registration_sources
    ADD COLUMN webhook_provider varchar(16) NOT NULL DEFAULT '',
    ADD COLUMN webhook_secret_encrypted text NOT NULL DEFAULT '',
    ADD CONSTRAINT gitops_registration_sources_webhook_provider_valid
        CHECK (webhook_provider IN ('', 'github'));

CREATE TABLE public.gitops_webhook_receipts (
    source_id uuid NOT NULL REFERENCES public.gitops_registration_sources(id) ON DELETE CASCADE,
    content_digest varchar(64) NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, content_digest)
);
-- GitHub signs the body, not the delivery ID or a freshness timestamp.
-- Retain authenticated-content receipts until the source is deleted: a TTL
-- would make an old captured signature valid again after the receipt expires.
