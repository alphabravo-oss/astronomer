CREATE TABLE control_plane_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(64) NOT NULL UNIQUE DEFAULT 'default',
    monitoring_queue_depth_threshold INTEGER NOT NULL DEFAULT 10,
    argocd_queue_depth_threshold INTEGER NOT NULL DEFAULT 10,
    tools_queue_depth_threshold INTEGER NOT NULL DEFAULT 10,
    catalog_queue_depth_threshold INTEGER NOT NULL DEFAULT 10,
    monitoring_stale_running_threshold INTEGER NOT NULL DEFAULT 1,
    argocd_stale_running_threshold INTEGER NOT NULL DEFAULT 1,
    tools_stale_running_threshold INTEGER NOT NULL DEFAULT 1,
    catalog_stale_running_threshold INTEGER NOT NULL DEFAULT 1,
    monitoring_recent_failure_threshold INTEGER NOT NULL DEFAULT 3,
    argocd_recent_failure_threshold INTEGER NOT NULL DEFAULT 3,
    tools_recent_failure_threshold INTEGER NOT NULL DEFAULT 3,
    catalog_recent_failure_threshold INTEGER NOT NULL DEFAULT 3,
    recent_failure_window_minutes INTEGER NOT NULL DEFAULT 30,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO control_plane_policies (name) VALUES ('default')
ON CONFLICT (name) DO NOTHING;

CREATE TABLE control_plane_alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    controller VARCHAR(32) NOT NULL,
    condition_type VARCHAR(32) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    message TEXT NOT NULL,
    detail JSONB NOT NULL DEFAULT '{}',
    fired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_control_plane_alerts_status ON control_plane_alerts (status, fired_at DESC);
CREATE UNIQUE INDEX idx_control_plane_alerts_active_unique
    ON control_plane_alerts (controller, condition_type)
    WHERE status = 'active';
