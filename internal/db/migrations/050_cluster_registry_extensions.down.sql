-- Reverse of 050_cluster_registry_extensions.up.sql. Drops the new columns
-- and the lookup index, then restores the legacy 1-per-cluster UNIQUE so a
-- rollback returns the table to its 001-initial shape.
--
-- Note: if multiple rows per cluster have already been inserted under 050,
-- the ADD CONSTRAINT will fail. Operators rolling back must first collapse
-- duplicates (DELETE all but one row per cluster_id) before applying this
-- file. Surfacing the conflict is intentional — silently dropping data on
-- rollback is worse than a noisy failure.

DROP INDEX IF EXISTS idx_cluster_registry_configs_cluster;

ALTER TABLE cluster_registry_configs
    DROP COLUMN IF EXISTS last_apply_error,
    DROP COLUMN IF EXISTS last_applied_at,
    DROP COLUMN IF EXISTS secret_name,
    DROP COLUMN IF EXISTS inject_default_sa,
    DROP COLUMN IF EXISTS namespaces;

ALTER TABLE cluster_registry_configs
    ADD CONSTRAINT cluster_registry_configs_cluster_id_key UNIQUE (cluster_id);
