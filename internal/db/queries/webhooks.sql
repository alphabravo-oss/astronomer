-- Webhook subscription + delivery queries (migration 048). Backs:
--
--   * /api/v1/admin/webhooks/* CRUD + test + deliveries view (handler)
--   * the event-bus tap that inserts a queued delivery on each matching
--     event (internal/webhook)
--   * the webhook:dispatch worker that drains pending rows into real
--     HTTP POSTs with HMAC signing + exponential backoff
--   * the daily retention sweep (default 30 days) that purges old
--     delivery rows so the table doesn't grow unbounded
--
-- secret_encrypted is the Fernet ciphertext of the HMAC signing secret;
-- this layer stores and returns it verbatim. Decryption happens in the
-- sender right before signing.

-- name: ListWebhookSubscriptions :many
SELECT * FROM webhook_subscriptions ORDER BY created_at DESC;

-- name: ListEnabledWebhookSubscriptions :many
-- Used by the event-bus tap: every published event scans this list and
-- filters by glob. Enabled-only because a disabled subscription should
-- NOT accumulate deliveries the dispatcher will never send (operators
-- toggle the flag while keeping the config row around).
SELECT * FROM webhook_subscriptions WHERE enabled = true ORDER BY created_at ASC;

-- name: GetWebhookSubscription :one
SELECT * FROM webhook_subscriptions WHERE id = $1;

-- name: GetWebhookSubscriptionByName :one
SELECT * FROM webhook_subscriptions WHERE name = $1;

-- name: CreateWebhookSubscription :one
INSERT INTO webhook_subscriptions (
    name, url, secret_encrypted, event_filters, payload_template,
    extra_headers, enabled, max_retries, timeout_seconds, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdateWebhookSubscription :one
-- Full replacement update (PUT semantics). Caller computes the merged
-- view and passes the final values; the handler is responsible for
-- preserving the existing secret when the admin didn't re-supply it
-- (analogous to smtp_settings password sentinel).
UPDATE webhook_subscriptions
SET name             = $2,
    url              = $3,
    secret_encrypted = $4,
    event_filters    = $5,
    payload_template = $6,
    extra_headers    = $7,
    enabled          = $8,
    max_retries      = $9,
    timeout_seconds  = $10,
    updated_at       = now()
WHERE id = $1
RETURNING *;

-- name: DeleteWebhookSubscription :exec
-- ON DELETE CASCADE on webhook_deliveries.subscription_id cleans up the
-- delivery history; the handler doesn't have to do that explicitly.
DELETE FROM webhook_subscriptions WHERE id = $1;

-- name: InsertWebhookDelivery :one
-- Tap-side insert. Called by the event-bus tap once per (subscription,
-- event) pair that matched at least one filter glob. status='queued'
-- and next_attempt_at=now() so the dispatcher picks it up on the very
-- next tick.
INSERT INTO webhook_deliveries (
    subscription_id, event_name, event_id, payload, payload_size,
    status, next_attempt_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetWebhookDelivery :one
SELECT * FROM webhook_deliveries WHERE id = $1;

-- name: ListWebhookDeliveriesBySubscription :many
-- Admin "recent deliveries" view. Newest-first; the handler caps
-- limit + offset.
SELECT * FROM webhook_deliveries
WHERE subscription_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountWebhookDeliveriesBySubscription :one
SELECT count(*) FROM webhook_deliveries WHERE subscription_id = $1;

-- name: ListPendingWebhookDeliveries :many
-- Dispatcher worker batch read. Returns rows the worker should attempt
-- this tick: status IN ('queued', 'failed') AND next_attempt_at <= now.
-- The partial index on next_attempt_at WHERE status IN ('queued','failed')
-- keeps this O(pending) regardless of table size.
SELECT * FROM webhook_deliveries
WHERE status IN ('queued', 'failed')
  AND (next_attempt_at IS NULL OR next_attempt_at <= $1)
ORDER BY next_attempt_at ASC NULLS FIRST, created_at ASC
LIMIT $2;

-- name: MarkWebhookDeliveryDelivered :exec
-- Final-state UPDATE on a 2xx. response_status/response_body capture
-- the receiver's reply for the admin view; the handler/sender caps
-- response_body to the first 4 KiB before this call.
UPDATE webhook_deliveries
SET status          = 'delivered',
    attempts        = $2,
    response_status = $3,
    response_body   = $4,
    delivered_at    = $5,
    next_attempt_at = NULL,
    last_error      = ''
WHERE id = $1;

-- name: MarkWebhookDeliveryFailed :exec
-- Records a delivery failure that is still retryable. The dispatcher
-- computes the next backoff slot and passes it via next_attempt_at.
UPDATE webhook_deliveries
SET status          = 'failed',
    attempts        = $2,
    response_status = $3,
    response_body   = $4,
    last_error      = $5,
    next_attempt_at = $6
WHERE id = $1;

-- name: MarkWebhookDeliveryDropped :exec
-- Terminal failure: either a permanent 4xx from the receiver (operator
-- has to fix the URL) or the retry budget was exhausted. next_attempt_at
-- is cleared so the row stops appearing in the pending list.
UPDATE webhook_deliveries
SET status          = 'dropped',
    attempts        = $2,
    response_status = $3,
    response_body   = $4,
    last_error      = $5,
    next_attempt_at = NULL
WHERE id = $1;

-- name: RetryWebhookDelivery :exec
-- Admin-triggered re-dispatch. Resets the row so the next dispatcher
-- tick picks it up immediately, regardless of where it was in the
-- backoff schedule.
UPDATE webhook_deliveries
SET status          = 'queued',
    next_attempt_at = $2,
    last_error      = ''
WHERE id = $1;

-- name: DeleteWebhookDeliveriesOlderThan :execrows
-- Retention sweep, runs daily. Returns the row count so the task can
-- emit an operator-visible "rows deleted" log line.
DELETE FROM webhook_deliveries WHERE created_at < $1;
