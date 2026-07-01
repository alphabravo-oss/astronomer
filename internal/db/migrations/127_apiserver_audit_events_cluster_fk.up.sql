-- apiserver_audit_events (migration 112) was created with a bare cluster_id
-- UUID column and NO foreign key to clusters(id). Two consequences:
--   1. Hard-deleting a cluster row leaves its audit rows orphaned — there is
--      no ON DELETE CASCADE to reap them — and
--   2. nothing enforces that an ingested cluster_id even names a real cluster.
-- Add the missing FK with ON DELETE CASCADE to match every other per-cluster
-- dependent table (e.g. control_plane_snapshots, migration 125).
--
-- NOTE: the normal decommission path TOMBSTONES (soft-deletes) the cluster row
-- rather than hard-deleting it, so the cascade does not fire on decommission;
-- phaseDeleteDependents still deletes these rows explicitly via
-- DeleteApiserverAuditEventsByCluster. This cascade only covers the true
-- hard-delete path and closes the orphan-integrity gap.

-- Reap any pre-existing orphans first so the constraint validates cleanly.
DELETE FROM apiserver_audit_events e
WHERE NOT EXISTS (SELECT 1 FROM clusters c WHERE c.id = e.cluster_id);

ALTER TABLE apiserver_audit_events
    ADD CONSTRAINT apiserver_audit_events_cluster_id_fkey
    FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE;
