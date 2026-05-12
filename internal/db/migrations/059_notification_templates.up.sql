-- Migration 059 — operator-tunable notification templates.
--
-- Each row is an OVERRIDE for a built-in template registered in the Go
-- code (internal/notify/templates.go). The registry supplies the
-- default subject/body; this table only stores diverged values so the
-- common case (no overrides) is byte-identical to pre-migration
-- behavior. A `notify.Resolve(key)` lookup that finds no row (or finds
-- enabled=false) falls back to the registry default.
--
-- template_key is the stable identifier — e.g. "email.password_reset",
-- "webhook.audit.event". Keys live in code; the migration does not
-- seed rows (defaults are sourced from the registry).
CREATE TABLE notification_templates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_key    VARCHAR(128) NOT NULL UNIQUE,
    channel         VARCHAR(16) NOT NULL,
    subject_tpl     TEXT NOT NULL DEFAULT '',
    body_tpl        TEXT NOT NULL,
    body_format     VARCHAR(16) NOT NULL DEFAULT 'markdown',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    updated_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT channel_valid CHECK (channel IN ('email','webhook')),
    CONSTRAINT body_format_valid CHECK (body_format IN ('text','markdown','html','json'))
);

CREATE INDEX notification_templates_channel_idx ON notification_templates(channel);
