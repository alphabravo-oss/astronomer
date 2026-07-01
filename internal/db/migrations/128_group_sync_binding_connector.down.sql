-- Rollback for 128_group_sync_binding_connector.up.sql.
DROP INDEX IF EXISTS idx_prb_group_sync_connector;
DROP INDEX IF EXISTS idx_crb_group_sync_connector;
DROP INDEX IF EXISTS idx_grb_group_sync_connector;

ALTER TABLE project_role_bindings DROP COLUMN IF EXISTS group_sync_connector_id;
ALTER TABLE cluster_role_bindings DROP COLUMN IF EXISTS group_sync_connector_id;
ALTER TABLE global_role_bindings  DROP COLUMN IF EXISTS group_sync_connector_id;
