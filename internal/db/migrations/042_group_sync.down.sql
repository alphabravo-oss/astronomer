-- Rollback for 042_group_sync.up.sql.

DROP INDEX IF EXISTS idx_grb_group_sync;
DROP INDEX IF EXISTS idx_crb_group_sync;
DROP INDEX IF EXISTS idx_prb_group_sync;

ALTER TABLE global_role_bindings  DROP COLUMN IF EXISTS source;
ALTER TABLE cluster_role_bindings DROP COLUMN IF EXISTS source;
ALTER TABLE project_role_bindings DROP COLUMN IF EXISTS source;

DROP TABLE IF EXISTS user_idp_groups;

DROP INDEX IF EXISTS idx_group_map_lookup;
DROP INDEX IF EXISTS uidx_group_map_unique;
DROP TABLE IF EXISTS identity_group_mappings;
