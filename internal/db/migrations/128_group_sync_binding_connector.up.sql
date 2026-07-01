-- Finding #3 (group-sync connector scoping).
--
-- The group-sync reconciler enumerates ALL of a user's source='group_sync'
-- bindings but computes the "wanted" set from only the connector being
-- synced. A user entitled under two connectors therefore loses the other
-- connector's roles on every login (they flap back on the next login
-- through that connector). Fixing it requires knowing WHICH connector
-- granted each group-sync binding, so both the enumeration and the delete
-- diff can be scoped to the connector currently syncing.
--
-- This adds the provenance column. It is nullable (legacy group-sync rows
-- and wildcard/NULL-connector mappings both carry NULL) and ON DELETE SET
-- NULL so removing a connector doesn't cascade-delete live bindings — the
-- next sync reconciles them. Non-blocking: a nullable ADD COLUMN with no
-- default does not rewrite the table (T30 lint).
ALTER TABLE global_role_bindings  ADD COLUMN group_sync_connector_id UUID REFERENCES dex_connectors(id) ON DELETE SET NULL;
ALTER TABLE cluster_role_bindings ADD COLUMN group_sync_connector_id UUID REFERENCES dex_connectors(id) ON DELETE SET NULL;
ALTER TABLE project_role_bindings ADD COLUMN group_sync_connector_id UUID REFERENCES dex_connectors(id) ON DELETE SET NULL;

-- Connector-scoped enumeration for the reconciler's revocation diff.
CREATE INDEX idx_grb_group_sync_connector ON global_role_bindings  (user_id, group_sync_connector_id) WHERE source = 'group_sync';
CREATE INDEX idx_crb_group_sync_connector ON cluster_role_bindings (user_id, group_sync_connector_id) WHERE source = 'group_sync';
CREATE INDEX idx_prb_group_sync_connector ON project_role_bindings (user_id, group_sync_connector_id) WHERE source = 'group_sync';
