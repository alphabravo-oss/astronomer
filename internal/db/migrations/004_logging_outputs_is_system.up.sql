-- Per-cluster Astronomer Loki destination. One system logging_outputs row
-- per member cluster (not fleet-wide). Bearer tokens stay in
-- loki_ingest_tokens; configuration JSONB is host/port/tls/tenant_id/labels
-- only.

ALTER TABLE public.logging_outputs
    ADD COLUMN is_system boolean DEFAULT false NOT NULL;

ALTER TABLE public.logging_outputs
    ADD CONSTRAINT logging_outputs_system_requires_cluster
    CHECK (NOT is_system OR cluster_id IS NOT NULL);

CREATE UNIQUE INDEX logging_outputs_one_system_per_cluster
    ON public.logging_outputs (cluster_id)
    WHERE is_system AND cluster_id IS NOT NULL;
