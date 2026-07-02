-- Index apiserver_audit_events.event_time so the retention prune is an index
-- range scan instead of a full sequential scan.
--
-- apiserver_audit_events is append-only and fleet-wide (one row per apiserver
-- request across every managed cluster), so it reaches millions of rows within
-- days. HandleApiserverAuditRetention runs PruneApiserverAuditEventsBefore
-- daily:  DELETE FROM apiserver_audit_events WHERE event_time < $cutoff.
--
-- The only pre-existing index is the composite (cluster_id, event_time DESC)
-- from migration 112. Because that index is led by cluster_id, a predicate on
-- event_time alone cannot use it, so the daily prune seq-scans the entire
-- table to find ~1 day of expired rows — a growing cost and dead-tuple/bloat
-- burden every single run. A plain btree on event_time turns the prune into a
-- bounded range scan over just the rows being deleted.
CREATE INDEX IF NOT EXISTS idx_apiserver_audit_events_event_time
    ON apiserver_audit_events (event_time);
