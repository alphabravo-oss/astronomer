-- Reverse 131: drop the event_time index used by the retention prune.
DROP INDEX IF EXISTS idx_apiserver_audit_events_event_time;
