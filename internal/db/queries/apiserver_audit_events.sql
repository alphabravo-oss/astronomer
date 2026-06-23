-- kube-apiserver audit-event ingest + read (migration 112).
--
-- The agent streams batched audit.k8s.io events to the management plane;
-- InsertApiserverAuditEvent is idempotent on (cluster_id, audit_id) so a
-- re-delivered batch is a no-op. ListApiserverAuditEventsByCluster powers
-- the operator read view.

-- name: InsertApiserverAuditEvent :exec
INSERT INTO apiserver_audit_events (
    cluster_id, audit_id, stage, verb, username, resource, namespace,
    status_code, event_time, raw
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (cluster_id, audit_id) DO NOTHING;

-- name: ListApiserverAuditEventsByCluster :many
SELECT id, cluster_id, audit_id, stage, verb, username, resource, namespace,
       status_code, event_time, raw, created_at
FROM apiserver_audit_events
WHERE cluster_id = $1
ORDER BY event_time DESC
LIMIT $2 OFFSET $3;

-- name: CountApiserverAuditEventsByCluster :one
SELECT count(*) FROM apiserver_audit_events WHERE cluster_id = $1;
