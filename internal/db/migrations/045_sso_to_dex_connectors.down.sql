-- Rollback for 045_sso_to_dex_connectors.up.sql.
--
-- Drops the legacy-prefixed rows we created, then removes the
-- `migrated_to_dex_at` column. Hand-edited dex_connectors that happen to
-- start with `legacy-` would also be removed; that name prefix is reserved
-- for the migration and the UI surfaces it as read-only.

DELETE FROM dex_connectors
WHERE name LIKE 'legacy-%';

ALTER TABLE sso_configurations
    DROP COLUMN IF EXISTS migrated_to_dex_at;
