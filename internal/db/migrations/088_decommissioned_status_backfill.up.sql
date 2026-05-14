-- Sprint 088 — data-fix for "ghost" decommissioned clusters.
--
-- Background: prior to the UpdateClusterStatus guard added alongside
-- this migration, the periodic health-check + metrics-publisher status
-- sweepers could race against the cluster_decommission reconciler. The
-- tombstone phase sets status='decommissioned', but a sweep that
-- observed the cluster before tombstone (then ran its UPDATE after)
-- would overwrite the status back to 'disconnected'. The decommissioned_at
-- timestamp remained set, but the status column drifted.
--
-- Observed on .247: 2 rows with decommissioned_at NOT NULL and
-- status='disconnected'. ListClusters filters decommissioned_at IS NULL
-- so the UI hid them, but operators querying clusters_by_status saw
-- ghost rows. Backfill any such drift; the query guard prevents future
-- regressions.

UPDATE clusters
   SET status = 'decommissioned'
 WHERE decommissioned_at IS NOT NULL
   AND status != 'decommissioned';
