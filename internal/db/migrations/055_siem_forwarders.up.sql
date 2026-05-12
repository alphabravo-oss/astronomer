-- External SIEM forwarders (migration 055).
--
-- Operators ship every audit row + domain event to syslog (RFC 5424),
-- Splunk HEC, or generic NDJSON-over-HTTPS sinks in real time. This is
-- distinct from outbound webhooks (migration 048): SIEM forwarders use
-- standard protocols, batch over a bounded per-forwarder queue, and the
-- delivery semantics are "the operator's SIEM is the source of truth"
-- rather than "fire and forget".
--
-- Three tables:
--
--   1. siem_forwarders — operator-managed config rows. One row per sink.
--      The auth blob (Splunk HEC token, generic-HTTPS bearer, syslog
--      shared key) is Fernet-encrypted at rest under auth.Encryptor; the
--      handler redacts it from GET responses via the `<encrypted>`
--      sentinel (same pattern as webhook secrets and SMTP passwords).
--
--   2. siem_forward_queue — per-forwarder bounded queue. The bus tap
--      INSERTs one row per matching (forwarder, event) pair; the worker
--      drains them in batches and DELETEs on success. Bounded by
--      `max_queue_size` (chart-tunable, default 10K); when full the
--      tap drops oldest rows and bumps `dropped_total` in the status
--      table.
--
--   3. siem_forwarder_status — per-forwarder health + lag. Updated by
--      the dispatcher every tick so the admin status endpoint can render
--      lag, last_error, and dropped_total without a COUNT(*) on the
--      queue table.
--
-- Schema constraints (T30 migration lint): every NOT NULL column carries
-- a DEFAULT so a re-run on an existing DB doesn't break ADD COLUMN; this
-- is a fresh CREATE TABLE so the constraint is informational here.

CREATE TABLE siem_forwarders (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(128) NOT NULL UNIQUE,
    -- "syslog_udp" | "syslog_tcp" | "syslog_tls" | "splunk_hec" | "ndjson_https"
    transport       VARCHAR(32) NOT NULL,
    endpoint        TEXT NOT NULL,           -- "logs.example.com:6514", "https://splunk:8088"
    -- Fernet-encrypted auth blob: { token: "...", username: "...", password: "..." }
    auth_encrypted  TEXT NOT NULL DEFAULT '',
    -- Event filter — fnmatch globs against event_name. Same shape as webhooks.
    event_filters   JSONB NOT NULL DEFAULT '[]',
    -- Format: "rfc5424" | "rfc3164" | "cef" | "ndjson"
    -- Auto-derived from transport; explicit lets operators force CEF on a TCP sink.
    format          VARCHAR(16) NOT NULL DEFAULT '',
    -- Connection knobs
    tls_skip_verify BOOLEAN NOT NULL DEFAULT false,
    ca_cert_pem     TEXT NOT NULL DEFAULT '',
    batch_size      INTEGER NOT NULL DEFAULT 100,
    flush_interval_ms INTEGER NOT NULL DEFAULT 2000,
    timeout_seconds INTEGER NOT NULL DEFAULT 10,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT transport_valid CHECK (transport IN ('syslog_udp','syslog_tcp','syslog_tls','splunk_hec','ndjson_https'))
);

-- Per-forwarder queue. Bounded by `max_queue_size` (chart-tunable, default
-- 10000). When full, oldest are dropped + a metric incremented. Status
-- transitions: pending -> dispatched | dropped.
CREATE TABLE siem_forward_queue (
    id              BIGSERIAL PRIMARY KEY,
    forwarder_id    UUID NOT NULL REFERENCES siem_forwarders(id) ON DELETE CASCADE,
    event_name      VARCHAR(128) NOT NULL,
    payload         JSONB NOT NULL,
    severity        VARCHAR(16) NOT NULL DEFAULT 'info',
    attempts        INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_siem_forward_queue_forwarder ON siem_forward_queue (forwarder_id, id);

-- Per-forwarder health + lag metric. Updated by the dispatcher.
CREATE TABLE siem_forwarder_status (
    forwarder_id    UUID PRIMARY KEY REFERENCES siem_forwarders(id) ON DELETE CASCADE,
    last_sent_at    TIMESTAMPTZ,
    last_error      TEXT NOT NULL DEFAULT '',
    queue_depth     INTEGER NOT NULL DEFAULT 0,
    dropped_total   BIGINT NOT NULL DEFAULT 0,
    dispatched_total BIGINT NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
