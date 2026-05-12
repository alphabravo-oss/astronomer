-- Down-migration for 061_project_catalogs.
--
-- Drops the subscriptions table FIRST (it has the FK to helm_repositories.
-- catalog_id) and then drops the owner_project_id column. Project-owned
-- catalog rows are deliberately NOT deleted by the down — operators
-- rolling backwards likely want to retain the data and just lose the
-- project-scoping; an explicit DELETE WHERE owner_project_id IS NOT NULL
-- is the operator's call.

DROP TABLE IF EXISTS project_catalog_subscriptions;
DROP INDEX IF EXISTS idx_helm_repositories_owner_project;
ALTER TABLE helm_repositories DROP COLUMN IF EXISTS owner_project_id;
