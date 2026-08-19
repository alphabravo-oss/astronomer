-- Named S3 (or S3-compatible) destinations for Astronomer's own pg_dump.
-- Operators add these from Settings → Astronomer backup; the server
-- reconciles one CronJob + credentials Secret per row. Credentials live
-- only in encrypted_credentials (Fernet). Multiple rows are intentional:
-- primary + DR bucket, or different schedules per bucket.

CREATE TABLE public.management_backup_destinations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(255) NOT NULL,
    bucket character varying(255) NOT NULL,
    prefix character varying(255) DEFAULT 'astronomer-pg'::character varying NOT NULL,
    region character varying(50) DEFAULT 'us-east-1'::character varying NOT NULL,
    endpoint_url character varying(500) DEFAULT ''::character varying NOT NULL,
    encrypted_credentials text DEFAULT ''::text NOT NULL,
    schedule character varying(100) DEFAULT '0 3 * * *'::character varying NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    keep_daily integer DEFAULT 30 NOT NULL,
    keep_weekly integer DEFAULT 12 NOT NULL,
    keep_monthly integer DEFAULT 6 NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);

ALTER TABLE public.management_backup_destinations
    ADD CONSTRAINT management_backup_destinations_pkey PRIMARY KEY (id);

CREATE UNIQUE INDEX management_backup_destinations_name_key
    ON public.management_backup_destinations (name);
