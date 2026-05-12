-- Reverse of 052_velero_snapshots.up.sql. cluster_restores' FK to
-- cluster_snapshots(id) cascades on the parent drop, so we just drop the
-- three tables in dependency order. Indexes go with the tables.

DROP TABLE IF EXISTS cluster_snapshot_schedules;
DROP TABLE IF EXISTS cluster_restores;
DROP TABLE IF EXISTS cluster_snapshots;
