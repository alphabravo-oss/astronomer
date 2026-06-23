-- Migration 109 — blessed-chart overlays sourced from the astronomer-catalog repo.
--
-- The catalog loader fetches catalog.yaml (ASTRONOMER_CATALOG_URL) and reconciles
-- one row here per blessed chart. These columns carry the curation metadata the
-- upstream Helm index can't provide: category, management-cluster safety, install
-- version policy, and optional display/icon overrides.
--
-- Keyed by (repo_url, chart_name) so the API can LEFT JOIN helm_charts onto it
-- without the catalog sync (which owns helm_charts) and the loader (which owns
-- this table) fighting over the same rows.
CREATE TABLE catalog_blessed_charts (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repo_url       VARCHAR(500) NOT NULL,
    chart_name     VARCHAR(255) NOT NULL,
    display_name   VARCHAR(255) NOT NULL DEFAULT '',
    description    TEXT NOT NULL DEFAULT '',
    category       VARCHAR(50) NOT NULL DEFAULT 'other',
    icon_url       VARCHAR(500) NOT NULL DEFAULT '',
    mgmt_safe      BOOLEAN NOT NULL DEFAULT true,
    version_policy VARCHAR(50) NOT NULL DEFAULT '',
    -- Ownership marker so the loader only reconciles rows it manages.
    source         VARCHAR(50) NOT NULL DEFAULT 'catalog.yaml',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (repo_url, chart_name)
);

CREATE INDEX idx_blessed_charts_category ON catalog_blessed_charts (category);
