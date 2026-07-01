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

-- name: InsertApiserverAuditEventsBatch :exec
-- Multi-row form of InsertApiserverAuditEvent: one round-trip for a whole
-- ingest batch (up to maxIngestEvents=1000) instead of 1000 sequential
-- INSERTs. Idempotent on (cluster_id, audit_id) exactly like the single-row
-- form. Column order matches the unnested array order below.
INSERT INTO apiserver_audit_events (
    cluster_id, audit_id, stage, verb, username, resource, namespace,
    status_code, event_time, raw
)
SELECT
    unnest(sqlc.arg(cluster_ids)::uuid[]),
    unnest(sqlc.arg(audit_ids)::text[]),
    unnest(sqlc.arg(stages)::text[]),
    unnest(sqlc.arg(verbs)::text[]),
    unnest(sqlc.arg(usernames)::text[]),
    unnest(sqlc.arg(resources)::text[]),
    unnest(sqlc.arg(namespaces)::text[]),
    unnest(sqlc.arg(status_codes)::int[]),
    unnest(sqlc.arg(event_times)::timestamptz[]),
    unnest(sqlc.arg(raws)::jsonb[])
ON CONFLICT (cluster_id, audit_id) DO NOTHING;

-- name: CountApiserverAuditEventsByClusterCapped :one
-- Capped count for the high-volume list view: stops scanning after max_rows so
-- the total never turns into an ever-slower full-index scan as the table grows
-- to millions of rows per cluster. The UI renders "N+" once the cap is hit.
SELECT count(*) FROM (
    SELECT 1 FROM apiserver_audit_events
    WHERE cluster_id = sqlc.arg(cluster_id)
    LIMIT sqlc.arg(max_rows)
) capped;

-- name: PruneApiserverAuditEventsBefore :execrows
-- Retention sweep: delete apiserver audit rows older than the cutoff. The table
-- is otherwise append-only and unbounded (one row per apiserver request, fleet
-- wide), so a periodic sweeper must call this to keep it from growing forever.
DELETE FROM apiserver_audit_events WHERE event_time < sqlc.arg(cutoff);
