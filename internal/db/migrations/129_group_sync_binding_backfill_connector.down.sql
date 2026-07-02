-- Rollback for 129_group_sync_binding_backfill_connector.up.sql.
--
-- This is a data-only backfill. We cannot reliably distinguish rows that
-- this migration stamped from rows a running system stamped afterwards via
-- CreateGroupSync*BindingForConnector, so clearing group_sync_connector_id
-- here would corrupt live provenance. Rolling back the *schema* (dropping
-- the column) is handled by 128_group_sync_binding_connector.down.sql;
-- rolling back only this backfill is intentionally a no-op.
SELECT 1;
