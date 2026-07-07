-- P-03 Alertmanager-style inhibition rules for control-plane / cluster alerts.
--
-- An inhibition suppresses dispatch of a firing "target" alert while a matching
-- "source" alert is currently firing and both share equal values on a set of
-- labels. This mirrors the control_plane_silences model (migration 013) but is
-- label-matcher driven instead of (controller, condition_type) scoped.
--
-- source_matchers / target_matchers are JSONB arrays of
--   { "label": string, "value": string, "is_regex": bool }
-- equal_labels is a JSONB array of label-name strings.
-- Defaults ('[]') keep inserts that omit them valid without a NOT NULL-without-
-- default rewrite of existing rows (there are none, but the DDL stays safe).
CREATE TABLE alert_inhibitions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    source_matchers JSONB NOT NULL DEFAULT '[]',
    target_matchers JSONB NOT NULL DEFAULT '[]',
    equal_labels JSONB NOT NULL DEFAULT '[]',
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_by_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_alert_inhibitions_enabled
    ON alert_inhibitions (enabled);
