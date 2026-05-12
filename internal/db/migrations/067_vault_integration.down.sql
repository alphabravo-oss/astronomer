-- Migration 067 DOWN — vault integration.
--
-- Drops the projects.default_vault_connection_id pointer first (so the
-- FK is gone before the referenced table) then drops vault_connections.

ALTER TABLE projects DROP COLUMN IF EXISTS default_vault_connection_id;
DROP TABLE IF EXISTS vault_connections;
