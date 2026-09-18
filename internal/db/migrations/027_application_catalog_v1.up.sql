-- Server-owned application catalog metadata. The existing Helm repository,
-- chart, version, and installation tables remain the normalized artifact and
-- lifecycle records; this migration turns catalog_blessed_charts into the
-- curated presentation/trust overlay instead of duplicating those records.
CREATE TABLE public.delivery_catalogs (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    name varchar(128) NOT NULL UNIQUE,
    display_name varchar(255) NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    channel varchar(32) NOT NULL DEFAULT 'stable',
    source_url text NOT NULL,
    source_revision varchar(128) NOT NULL DEFAULT '',
    index_digest varchar(71) NOT NULL,
    verification_status varchar(32) NOT NULL,
    verification_identity text NOT NULL DEFAULT '',
    trust_policy jsonb NOT NULL DEFAULT '{}',
    last_sync_attempted_at timestamptz NOT NULL DEFAULT now(),
    last_synced_at timestamptz NOT NULL DEFAULT now(),
    last_sync_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT delivery_catalogs_digest_check
        CHECK (index_digest ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT delivery_catalogs_verification_check
        CHECK (verification_status IN ('verified', 'digest-verified', 'unsigned', 'failed', 'revoked'))
);

ALTER TABLE public.catalog_blessed_charts
    ADD COLUMN slug varchar(128) NOT NULL DEFAULT '',
    ADD COLUMN repo_name varchar(128) NOT NULL DEFAULT '',
    ADD COLUMN support_tier varchar(32) NOT NULL DEFAULT 'upstream',
    ADD COLUMN featured boolean NOT NULL DEFAULT false,
    ADD COLUMN privileged boolean NOT NULL DEFAULT false,
    ADD COLUMN default_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN documentation_url text NOT NULL DEFAULT '',
    ADD COLUMN presentation jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN artifact jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN compatibility jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN resources jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN storage jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN lifecycle jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN raw_entry jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN catalog_digest varchar(71) NOT NULL DEFAULT '',
    ADD COLUMN verification_status varchar(32) NOT NULL DEFAULT 'unsigned',
    ADD COLUMN verification_identity text NOT NULL DEFAULT '',
    ADD COLUMN revoked boolean NOT NULL DEFAULT false;

CREATE UNIQUE INDEX catalog_blessed_charts_slug_source_key
    ON public.catalog_blessed_charts (source, slug)
    WHERE slug <> '';
CREATE INDEX idx_catalog_blessed_charts_facets
    ON public.catalog_blessed_charts (support_tier, featured, privileged, revoked);

CREATE TABLE public.delivery_application_versions (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    installation_id uuid NOT NULL REFERENCES public.installed_charts(id) ON DELETE CASCADE,
    chart_version_id uuid NOT NULL REFERENCES public.helm_chart_versions(id) ON DELETE RESTRICT,
    bundle_version_id uuid REFERENCES public.component_bundle_versions(id) ON DELETE SET NULL,
    catalog_slug varchar(128) NOT NULL,
    version varchar(100) NOT NULL,
    artifact_digest varchar(71) NOT NULL,
    values_digest varchar(71) NOT NULL,
    verification_status varchar(32) NOT NULL DEFAULT 'digest-verified',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (installation_id, chart_version_id, values_digest)
);

CREATE INDEX idx_delivery_application_versions_installation
    ON public.delivery_application_versions (installation_id, created_at DESC);

