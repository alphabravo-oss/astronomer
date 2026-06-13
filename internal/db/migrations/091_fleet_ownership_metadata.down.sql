DROP INDEX IF EXISTS projects_managed_by_idx;
DROP INDEX IF EXISTS clusters_managed_by_idx;
DROP INDEX IF EXISTS projects_external_ref_unique;
DROP INDEX IF EXISTS clusters_external_ref_unique;

ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_external_ref_all_or_none;
ALTER TABLE clusters DROP CONSTRAINT IF EXISTS clusters_external_ref_all_or_none;
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_managed_by_valid;
ALTER TABLE clusters DROP CONSTRAINT IF EXISTS clusters_managed_by_valid;

ALTER TABLE projects
    DROP COLUMN IF EXISTS observed_generation,
    DROP COLUMN IF EXISTS external_ref_name,
    DROP COLUMN IF EXISTS external_ref_namespace,
    DROP COLUMN IF EXISTS external_ref_kind,
    DROP COLUMN IF EXISTS external_ref_api_version,
    DROP COLUMN IF EXISTS managed_by;

ALTER TABLE clusters
    DROP COLUMN IF EXISTS observed_generation,
    DROP COLUMN IF EXISTS external_ref_name,
    DROP COLUMN IF EXISTS external_ref_namespace,
    DROP COLUMN IF EXISTS external_ref_kind,
    DROP COLUMN IF EXISTS external_ref_api_version,
    DROP COLUMN IF EXISTS managed_by;
