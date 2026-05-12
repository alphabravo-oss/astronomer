-- SIEM forwarder + queue + status queries (migration 055). Backs:
--
--   * /api/v1/admin/siem-forwarders/* CRUD + test + status (handler)
--   * the event-bus tap that enqueues matching events onto the per-
--     forwarder queue (internal/siem.BusTap)
--   * the worker dispatcher that batches + ships queue rows to the
--     forwarder's transport, deletes on success, and updates the
--     status row (queue_depth + dispatched_total + last_sent_at)
--   * the daily retention sweep that purges queue rows older than 7
--     days regardless of forwarder status so stale forwarders don't
--     pin disk
--
-- auth_encrypted is the Fernet ciphertext of the JSON auth blob; this
-- layer stores and returns it verbatim. Decryption happens in the
-- dispatcher right before the per-tick connect.

-- name: ListSIEMForwarders :many
SELECT id, name, transport, endpoint, auth_encrypted, event_filters, format,
       tls_skip_verify, ca_cert_pem, batch_size, flush_interval_ms,
       timeout_seconds, enabled, created_by, created_at, updated_at
FROM siem_forwarders ORDER BY created_at DESC;

-- name: ListEnabledSIEMForwarders :many
-- Used by the event-bus tap: every published event scans this list and
-- filters by glob. Enabled-only because a disabled forwarder should NOT
-- accumulate queue rows the dispatcher will never drain.
SELECT id, name, transport, endpoint, auth_encrypted, event_filters, format,
       tls_skip_verify, ca_cert_pem, batch_size, flush_interval_ms,
       timeout_seconds, enabled, created_by, created_at, updated_at
FROM siem_forwarders WHERE enabled = true ORDER BY created_at ASC;

-- name: GetSIEMForwarder :one
SELECT id, name, transport, endpoint, auth_encrypted, event_filters, format,
       tls_skip_verify, ca_cert_pem, batch_size, flush_interval_ms,
       timeout_seconds, enabled, created_by, created_at, updated_at
FROM siem_forwarders WHERE id = $1;

-- name: GetSIEMForwarderByName :one
SELECT id, name, transport, endpoint, auth_encrypted, event_filters, format,
       tls_skip_verify, ca_cert_pem, batch_size, flush_interval_ms,
       timeout_seconds, enabled, created_by, created_at, updated_at
FROM siem_forwarders WHERE name = $1;

-- name: CreateSIEMForwarder :one
INSERT INTO siem_forwarders (
    name, transport, endpoint, auth_encrypted, event_filters, format,
    tls_skip_verify, ca_cert_pem, batch_size, flush_interval_ms,
    timeout_seconds, enabled, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING id, name, transport, endpoint, auth_encrypted, event_filters, format,
          tls_skip_verify, ca_cert_pem, batch_size, flush_interval_ms,
          timeout_seconds, enabled, created_by, created_at, updated_at;

-- name: UpdateSIEMForwarder :one
-- Full replacement update (PUT semantics). The handler preserves the
-- existing auth_encrypted when the admin didn't re-supply it (sentinel
-- pattern, analogous to webhook_subscriptions.secret_encrypted).
UPDATE siem_forwarders
SET name              = $2,
    transport         = $3,
    endpoint          = $4,
    auth_encrypted    = $5,
    event_filters     = $6,
    format            = $7,
    tls_skip_verify   = $8,
    ca_cert_pem       = $9,
    batch_size        = $10,
    flush_interval_ms = $11,
    timeout_seconds   = $12,
    enabled           = $13,
    updated_at        = now()
WHERE id = $1
RETURNING id, name, transport, endpoint, auth_encrypted, event_filters, format,
          tls_skip_verify, ca_cert_pem, batch_size, flush_interval_ms,
          timeout_seconds, enabled, created_by, created_at, updated_at;

-- name: DeleteSIEMForwarder :exec
-- ON DELETE CASCADE on siem_forward_queue.forwarder_id +
-- siem_forwarder_status.forwarder_id cleans up the queued events + the
-- status row; the handler doesn't have to do that explicitly.
DELETE FROM siem_forwarders WHERE id = $1;

-- name: EnqueueSIEMEvent :one
-- Bus-tap insert. Called once per (forwarder, event) pair that matched
-- at least one filter glob. The dispatcher picks rows up in batch
-- order via the (forwarder_id, id) index.
INSERT INTO siem_forward_queue (
    forwarder_id, event_name, payload, severity
) VALUES ($1, $2, $3, $4)
RETURNING id, forwarder_id, event_name, payload, severity, attempts, created_at;

-- name: ListSIEMQueueBatch :many
-- Dispatcher batch read. Ordered by id ascending so the dispatcher
-- processes oldest-first and the per-forwarder partial index serves
-- this query in constant time.
SELECT id, forwarder_id, event_name, payload, severity, attempts, created_at
FROM siem_forward_queue
WHERE forwarder_id = $1
ORDER BY id ASC
LIMIT $2;

-- name: DeleteSIEMQueueByIDs :exec
-- Called after a successful batch send. The dispatcher computes the id
-- set from the rows it just shipped.
DELETE FROM siem_forward_queue WHERE id = ANY($1::bigint[]);

-- name: IncrementSIEMQueueAttempts :exec
-- Bumps the per-row retry counter without changing payload/severity. The
-- dispatcher calls this on each failure; when attempts crosses the cap
-- (100) the row is force-deleted via DeleteSIEMQueueByIDs and the
-- forwarder's dropped_total counter is bumped.
UPDATE siem_forward_queue
SET attempts = attempts + 1
WHERE id = ANY($1::bigint[]);

-- name: ListSIEMQueueExhausted :many
-- Returns rows that have hit the retry cap. The dispatcher deletes
-- these + counts them as dropped — they aren't going to succeed and
-- holding them in the queue starves the rest of the batch.
SELECT id, forwarder_id, event_name, payload, severity, attempts, created_at
FROM siem_forward_queue
WHERE forwarder_id = $1 AND attempts >= $2
ORDER BY id ASC
LIMIT $3;

-- name: CountSIEMQueueByForwarder :one
SELECT count(*) FROM siem_forward_queue WHERE forwarder_id = $1;

-- name: ListOldestSIEMQueue :many
-- Used by the tap when the queue depth hits the chart-tunable cap. We
-- delete the oldest N rows to make room for the new ones.
SELECT id FROM siem_forward_queue
WHERE forwarder_id = $1
ORDER BY id ASC
LIMIT $2;

-- name: DeleteSIEMQueueOlderThan :execrows
-- Daily retention sweep. Removes queue rows older than the cutoff
-- regardless of forwarder status so a stuck/disabled forwarder doesn't
-- pin disk.
DELETE FROM siem_forward_queue WHERE created_at < $1;

-- name: UpsertSIEMForwarderStatus :exec
-- Called by the dispatcher after each tick. The composite parameters
-- carry the deltas the dispatcher computed for this tick; the existing
-- row is preserved on conflict so the cumulative totals accumulate.
INSERT INTO siem_forwarder_status (
    forwarder_id, last_sent_at, last_error, queue_depth,
    dropped_total, dispatched_total, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (forwarder_id) DO UPDATE
SET last_sent_at      = COALESCE(EXCLUDED.last_sent_at, siem_forwarder_status.last_sent_at),
    last_error        = EXCLUDED.last_error,
    queue_depth       = EXCLUDED.queue_depth,
    dropped_total     = siem_forwarder_status.dropped_total + EXCLUDED.dropped_total,
    dispatched_total  = siem_forwarder_status.dispatched_total + EXCLUDED.dispatched_total,
    updated_at        = now();

-- name: GetSIEMForwarderStatus :one
SELECT forwarder_id, last_sent_at, last_error, queue_depth, dropped_total,
       dispatched_total, updated_at
FROM siem_forwarder_status WHERE forwarder_id = $1;
