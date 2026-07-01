ALTER TABLE apiserver_audit_events
    DROP CONSTRAINT IF EXISTS apiserver_audit_events_cluster_id_fkey;
