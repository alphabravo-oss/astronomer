-- Down-migration for 058_dashboard_widgets.
--
-- Drop the widget definitions first (no FKs into it), then the
-- datasources table, then drop the clusters.cluster_uid column. The
-- column drop is IF EXISTS for symmetry with the additive up path —
-- a partial rollback that already dropped the column shouldn't fail
-- the rest of the down migration.

DROP TABLE IF EXISTS dashboard_widgets;
DROP TABLE IF EXISTS prometheus_datasources;
ALTER TABLE clusters DROP COLUMN IF EXISTS cluster_uid;
