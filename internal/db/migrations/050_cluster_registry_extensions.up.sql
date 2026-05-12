-- Cluster registry credential management for member clusters (migration 050).
--
-- The original cluster_registry_configs table (migration 001) was modeled as a
-- 1-per-cluster singleton: cluster_id was UNIQUE, and the single Upsert query
-- replaced the row in place. That shape was fine while the UI exposed exactly
-- one "private registry" knob per cluster, but it doesn't fit the Rancher
-- "Cluster → Registries" tab — operators routinely add several pull secrets
-- per cluster (one per upstream registry, e.g. registry.access.redhat.com +
-- quay.io + a private Artifactory).
--
-- This migration:
--
--   1. Adds the columns the multi-registry handler + apply worker need:
--        - namespaces        JSONB scope ([] = every project namespace under
--          this cluster, explicit list = only those names)
--        - inject_default_sa BOOLEAN whether to patch the namespace's `default`
--          ServiceAccount with the Secret as imagePullSecrets (so workloads
--          authenticate without per-Deployment imagePullSecrets boilerplate)
--        - secret_name       VARCHAR the Secret name applied in-cluster; empty
--          means the server picks `astronomer-registry-<id>` at apply time
--        - last_applied_at   TIMESTAMPTZ stamped by the worker on success
--        - last_apply_error  TEXT  stamped by the worker on failure
--
--   2. Drops the UNIQUE (cluster_id) constraint so the table can hold many
--      rows per cluster. The previous Upsert query (which targets the legacy
--      singleton path) is preserved in queries/clusters.sql for back-compat
--      with the old `PUT /clusters/{id}/registry/` route; the new multi-row
--      CRUD is wired against `id` as the primary identifier.
--
-- Conventions:
--   - All NEW columns are NOT NULL DEFAULT … on the same line, satisfying the
--     T30 ADD-COLUMN-NULLABILITY lint (check-migrations.sh).
--   - The constraint drop is gated on its existence — production may have a
--     fresh install where the constraint sits under a different name, and
--     fresh installs that ran 001 will have the auto-generated
--     `cluster_registry_configs_cluster_id_key`. Both are handled.

ALTER TABLE cluster_registry_configs
    ADD COLUMN IF NOT EXISTS namespaces        JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS inject_default_sa BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS secret_name       VARCHAR(128) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_applied_at   TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_apply_error  TEXT NOT NULL DEFAULT '';

-- Drop the legacy 1-per-cluster constraint so multiple registry configs can
-- co-exist per cluster. The constraint name is the Postgres default for the
-- column-level UNIQUE in 001; if it's been renamed in a fork, the IF EXISTS
-- keeps this idempotent.
ALTER TABLE cluster_registry_configs
    DROP CONSTRAINT IF EXISTS cluster_registry_configs_cluster_id_key;

-- The new multi-row CRUD lists/looks-up by cluster_id, so make sure the
-- planner has an index after the UNIQUE constraint is gone.
CREATE INDEX IF NOT EXISTS idx_cluster_registry_configs_cluster
    ON cluster_registry_configs (cluster_id);
