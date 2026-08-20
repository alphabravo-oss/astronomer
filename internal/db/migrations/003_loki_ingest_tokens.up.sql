-- Hosted Loki ingest tokens. Hash-only for loki-auth verification; Fernet
-- ciphertext is kept so the member Fluent Bit ConfigMap can be re-rendered
-- after rotate. Do not encrypt existing logging_outputs.configuration here.

CREATE TABLE public.loki_ingest_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    token_hash text NOT NULL,
    token_encrypted text NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    rotated_at timestamptz DEFAULT now() NOT NULL,
    created_by_id uuid
);

ALTER TABLE public.loki_ingest_tokens
    ADD CONSTRAINT loki_ingest_tokens_pkey PRIMARY KEY (id);

ALTER TABLE public.loki_ingest_tokens
    ADD CONSTRAINT loki_ingest_tokens_cluster_id_key UNIQUE (cluster_id);

ALTER TABLE public.loki_ingest_tokens
    ADD CONSTRAINT loki_ingest_tokens_cluster_id_fkey
    FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;

ALTER TABLE public.loki_ingest_tokens
    ADD CONSTRAINT loki_ingest_tokens_created_by_id_fkey
    FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;
