-- Outbound webhook subscriptions (migration 048).
--
-- Astronomer-go already publishes lifecycle events to an in-process bus
-- (internal/events.Bus) consumed by the SSE stream. This migration adds
-- a durable fan-out path: operators register a subscription with a target
-- URL + HMAC signing secret + event-name glob filter, and a worker tap
-- POSTs matching events to the URL with retries + delivery telemetry.
--
-- Use cases include forwarding to Slack incoming-webhook URLs, PagerEvents,
-- an internal SIEM, or a customer-built CMDB.
--
-- Two tables:
--
--   1. webhook_subscriptions — the operator-managed config rows. The HMAC
--      signing secret is Fernet-encrypted under auth.Encryptor (same key
--      set + rotation procedure as smtp_settings.password_encrypted and
--      every other encrypted-at-rest column).
--
--   2. webhook_deliveries — one row per (subscription, event) attempt.
--      The dispatcher picks these up on a 15s tick and ships the body to
--      the URL; status moves through queued → delivered/failed/dropped
--      with attempts + last_error stamped for the admin "recent
--      deliveries" view. Old rows are swept daily (default 30d retention,
--      operator-tunable via platform_settings 'webhook.delivery_retention_days').
--
-- Schema constraints (T30 migration lint): every NOT NULL column carries
-- a DEFAULT so a re-run on an existing DB doesn't break ADD COLUMN; this
-- is a fresh CREATE TABLE so the constraint is informational here.

CREATE TABLE webhook_subscriptions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(128) NOT NULL,
    url             TEXT NOT NULL,
    -- Fernet-encrypted HMAC signing secret. Receivers verify by
    -- recomputing HMAC-SHA256 over the request body.
    secret_encrypted TEXT NOT NULL,
    -- JSON array of event-name globs: ["audit.*", "cluster.*", "auth.login_failed"]
    event_filters   JSONB NOT NULL DEFAULT '[]',
    -- Optional Go-template applied to the event JSON; empty = ship raw event.
    payload_template TEXT NOT NULL DEFAULT '',
    -- Optional HTTP headers attached to every delivery. JSON object.
    -- Common use: { "Content-Type": "application/json" } (default
    -- when empty) or auth tokens for the receiver.
    extra_headers   JSONB NOT NULL DEFAULT '{}',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    -- Per-subscription retry config.
    max_retries     INTEGER NOT NULL DEFAULT 5,
    timeout_seconds INTEGER NOT NULL DEFAULT 10,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uidx_webhook_subscriptions_name ON webhook_subscriptions (name);

CREATE TABLE webhook_deliveries (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id UUID NOT NULL REFERENCES webhook_subscriptions(id) ON DELETE CASCADE,
    event_name      VARCHAR(128) NOT NULL,
    event_id        VARCHAR(128) NOT NULL DEFAULT '',
    -- Raw or templated payload bytes the dispatcher will POST. Held on
    -- the row so a worker restart can resume mid-retry-schedule without
    -- losing the event.
    payload         JSONB NOT NULL DEFAULT '{}',
    payload_size    INTEGER NOT NULL DEFAULT 0,
    -- "queued" | "delivered" | "failed" | "dropped"
    status          VARCHAR(16) NOT NULL DEFAULT 'queued',
    attempts        INTEGER NOT NULL DEFAULT 0,
    response_status INTEGER NOT NULL DEFAULT 0,
    response_body   TEXT NOT NULL DEFAULT '',   -- truncated to first 4KB
    last_error      TEXT NOT NULL DEFAULT '',
    delivered_at    TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_webhook_deliveries_sub_recent ON webhook_deliveries (subscription_id, created_at DESC);
CREATE INDEX idx_webhook_deliveries_pending ON webhook_deliveries (next_attempt_at) WHERE status IN ('queued', 'failed');
