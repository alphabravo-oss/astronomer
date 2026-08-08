-- Product-owned Charlie finding alert policy and durable delivery ledger.
-- Channel credentials remain in Astronomer's existing notification_channels;
-- Charlie central never receives thresholds, recipients, or delivery secrets.

CREATE TABLE charlie_alert_policies (
    connection_id             UUID PRIMARY KEY REFERENCES charlie_connections(id) ON DELETE CASCADE,
    enabled                   BOOLEAN NOT NULL DEFAULT true,
    minimum_severity          VARCHAR(16) NOT NULL DEFAULT 'medium',
    dedupe_window_seconds     INTEGER NOT NULL DEFAULT 1800,
    escalation_after_seconds INTEGER NOT NULL DEFAULT 3600,
    quiet_hours_enabled       BOOLEAN NOT NULL DEFAULT false,
    quiet_hours_start         CHAR(5) NOT NULL DEFAULT '22:00',
    quiet_hours_end           CHAR(5) NOT NULL DEFAULT '07:00',
    quiet_hours_timezone      VARCHAR(64) NOT NULL DEFAULT 'UTC',
    revision                  BIGINT NOT NULL DEFAULT 1,
    updated_by_id             UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT charlie_alert_policy_severity CHECK (minimum_severity IN ('info', 'low', 'medium', 'warning', 'high', 'critical')),
    CONSTRAINT charlie_alert_policy_dedupe CHECK (dedupe_window_seconds BETWEEN 60 AND 604800),
    CONSTRAINT charlie_alert_policy_escalation CHECK (escalation_after_seconds = 0 OR escalation_after_seconds BETWEEN 300 AND 604800),
    CONSTRAINT charlie_alert_policy_quiet_start CHECK (quiet_hours_start ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
    CONSTRAINT charlie_alert_policy_quiet_end CHECK (quiet_hours_end ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
    CONSTRAINT charlie_alert_policy_timezone_nonempty CHECK (length(trim(quiet_hours_timezone)) > 0),
    CONSTRAINT charlie_alert_policy_revision CHECK (revision > 0)
);

CREATE TABLE charlie_alert_policy_channels (
    connection_id          UUID NOT NULL REFERENCES charlie_alert_policies(connection_id) ON DELETE CASCADE,
    notification_channel_id UUID NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (connection_id, notification_channel_id)
);

CREATE TABLE charlie_alert_deliveries (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id           UUID NOT NULL REFERENCES charlie_connections(id) ON DELETE CASCADE,
    finding_id              UUID NOT NULL REFERENCES charlie_findings(id) ON DELETE CASCADE,
    notification_channel_id UUID REFERENCES notification_channels(id) ON DELETE SET NULL,
    policy_revision         BIGINT NOT NULL,
    delivery_kind           VARCHAR(16) NOT NULL,
    dedupe_bucket           BIGINT NOT NULL,
    severity                VARCHAR(16) NOT NULL,
    status                  VARCHAR(16) NOT NULL DEFAULT 'queued',
    attempt_count           INTEGER NOT NULL DEFAULT 0,
    maximum_attempts        INTEGER NOT NULL DEFAULT 8,
    next_attempt_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at            TIMESTAMPTZ,
    last_error_code         VARCHAR(64) NOT NULL DEFAULT '',
    deep_link               VARCHAR(256) NOT NULL,
    subject                 VARCHAR(256) NOT NULL,
    body                    VARCHAR(1024) NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT charlie_alert_delivery_kind CHECK (delivery_kind IN ('initial', 'escalation')),
    CONSTRAINT charlie_alert_delivery_severity CHECK (severity IN ('info', 'low', 'medium', 'warning', 'high', 'critical')),
    CONSTRAINT charlie_alert_delivery_status CHECK (status IN ('queued', 'delivering', 'retry', 'delivered', 'suppressed', 'dead')),
    CONSTRAINT charlie_alert_delivery_attempts CHECK (attempt_count >= 0 AND maximum_attempts BETWEEN 1 AND 20),
    CONSTRAINT charlie_alert_delivery_revision CHECK (policy_revision > 0),
    CONSTRAINT charlie_alert_delivery_deep_link CHECK (deep_link ~ '^/dashboard/charlie\?tab=findings&finding=[0-9a-f-]{36}$'),
    UNIQUE (finding_id, notification_channel_id, delivery_kind, dedupe_bucket)
);

CREATE INDEX charlie_alert_deliveries_due_idx
    ON charlie_alert_deliveries (next_attempt_at, created_at)
    WHERE status IN ('queued', 'retry');
CREATE INDEX charlie_alert_deliveries_finding_idx
    ON charlie_alert_deliveries (finding_id, created_at DESC);
