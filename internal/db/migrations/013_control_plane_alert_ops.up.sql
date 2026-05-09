ALTER TABLE control_plane_alerts
    ADD COLUMN acknowledged_by_id UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN acknowledged_at TIMESTAMPTZ;

CREATE TABLE control_plane_silences (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    controller VARCHAR(32) NOT NULL,
    condition_type VARCHAR(32) NOT NULL DEFAULT '',
    reason TEXT NOT NULL,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    created_by_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_control_plane_silences_window
    ON control_plane_silences (starts_at, ends_at);
