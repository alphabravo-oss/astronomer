-- Explicit ownership metadata for Postgres rows that may be driven by REST/UI,
-- CRDs, system reconcilers, or ArgoCD. This is intentionally narrow: product
-- identity, audit, RBAC, and credential rows stay Postgres-owned and do not get
-- mirrored into CRDs.

ALTER TABLE clusters
    ADD COLUMN IF NOT EXISTS managed_by VARCHAR(16) NOT NULL DEFAULT 'api',
    ADD COLUMN IF NOT EXISTS external_ref_api_version VARCHAR(128) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS external_ref_kind VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS external_ref_namespace VARCHAR(253) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS external_ref_name VARCHAR(253) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS observed_generation BIGINT NOT NULL DEFAULT 0;

ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS managed_by VARCHAR(16) NOT NULL DEFAULT 'api',
    ADD COLUMN IF NOT EXISTS external_ref_api_version VARCHAR(128) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS external_ref_kind VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS external_ref_namespace VARCHAR(253) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS external_ref_name VARCHAR(253) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS observed_generation BIGINT NOT NULL DEFAULT 0;

ALTER TABLE clusters DROP CONSTRAINT IF EXISTS clusters_managed_by_valid;
ALTER TABLE clusters ADD CONSTRAINT clusters_managed_by_valid
    CHECK (managed_by IN ('ui', 'api', 'crd', 'system', 'argocd'));

ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_managed_by_valid;
ALTER TABLE projects ADD CONSTRAINT projects_managed_by_valid
    CHECK (managed_by IN ('ui', 'api', 'crd', 'system', 'argocd'));

ALTER TABLE clusters DROP CONSTRAINT IF EXISTS clusters_external_ref_all_or_none;
ALTER TABLE clusters ADD CONSTRAINT clusters_external_ref_all_or_none
    CHECK (
        (external_ref_api_version = '' AND external_ref_kind = '' AND external_ref_namespace = '' AND external_ref_name = '')
        OR
        (external_ref_api_version <> '' AND external_ref_kind <> '' AND external_ref_namespace <> '' AND external_ref_name <> '')
    );

ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_external_ref_all_or_none;
ALTER TABLE projects ADD CONSTRAINT projects_external_ref_all_or_none
    CHECK (
        (external_ref_api_version = '' AND external_ref_kind = '' AND external_ref_namespace = '' AND external_ref_name = '')
        OR
        (external_ref_api_version <> '' AND external_ref_kind <> '' AND external_ref_namespace <> '' AND external_ref_name <> '')
    );

CREATE UNIQUE INDEX IF NOT EXISTS clusters_external_ref_unique
    ON clusters (external_ref_api_version, external_ref_kind, external_ref_namespace, external_ref_name)
    WHERE external_ref_name <> '' AND decommissioned_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS projects_external_ref_unique
    ON projects (external_ref_api_version, external_ref_kind, external_ref_namespace, external_ref_name)
    WHERE external_ref_name <> '';

CREATE INDEX IF NOT EXISTS clusters_managed_by_idx ON clusters (managed_by);
CREATE INDEX IF NOT EXISTS projects_managed_by_idx ON projects (managed_by);
