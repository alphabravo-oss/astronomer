-- Service mesh detection state (migration 071).
--
-- Sprint 071 surfaces a "Service mesh" tab on the cluster-detail page.
-- The handler reads cluster_service_mesh, the worker task
-- (mesh:detect, 5m cadence) writes it. One row per cluster; the row
-- carries the detected mesh + version + the aggregate counts that
-- drive the at-a-glance health tiles. No mutation of the cluster
-- happens from this surface — read-only v1.
--
-- Why a separate table instead of stuffing fields on clusters:
--   - Detection runs on its own cadence and may fail independently of
--     the cluster-health probe. Keeping the rows separate means a
--     failed detection stamps last_error here without disturbing the
--     cluster.status column the connect/health path drives.
--   - The aggregate counts can churn on every detection; isolating
--     them keeps the hot clusters row narrow.
--
-- Migration safety:
--   - All NOT NULL columns carry a DEFAULT on the same line so the
--     check-migrations.sh T30 lint passes and a future ADD COLUMN on
--     a populated installation doesn't take an ACCESS EXCLUSIVE lock.
--   - ON DELETE CASCADE on the cluster FK so dropping a cluster
--     cleanly drops its mesh row.
--   - helm_chart_tags is created IF NOT EXISTS — sister sprints may
--     have introduced it; we want this migration to apply cleanly
--     either way.
--   - The INSERT into helm_chart_tags is ON CONFLICT DO NOTHING so
--     re-running the migration (and re-running on installs that
--     already have these tag rows) is a no-op.

CREATE TABLE IF NOT EXISTS helm_chart_tags (
    chart_id UUID NOT NULL REFERENCES helm_charts(id) ON DELETE CASCADE,
    tag      VARCHAR(64) NOT NULL,
    PRIMARY KEY (chart_id, tag)
);
CREATE INDEX IF NOT EXISTS idx_helm_chart_tags_tag ON helm_chart_tags (tag);

CREATE TABLE cluster_service_mesh (
    cluster_id      UUID PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
    -- "istio" | "linkerd" | "kuma" | "cilium" | "none" | "unknown"
    detected_mesh   VARCHAR(32) NOT NULL DEFAULT 'unknown',
    detected_version VARCHAR(64) NOT NULL DEFAULT '',
    -- Namespace where the mesh control plane lives (e.g. istio-system).
    control_plane_namespace VARCHAR(253) NOT NULL DEFAULT '',
    -- Aggregate health counts as of last detection.
    gateway_count       INTEGER NOT NULL DEFAULT 0,
    virtual_service_count INTEGER NOT NULL DEFAULT 0,
    destination_rule_count INTEGER NOT NULL DEFAULT 0,
    peer_authentication_count INTEGER NOT NULL DEFAULT 0,
    service_profile_count INTEGER NOT NULL DEFAULT 0,
    server_auth_count   INTEGER NOT NULL DEFAULT 0,
    -- mTLS coverage % (namespaces with peer-authentication ÷ total). Rough.
    mtls_coverage_pct INTEGER NOT NULL DEFAULT 0,
    last_detected_at TIMESTAMPTZ,
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT detected_mesh_valid CHECK (detected_mesh IN ('istio','linkerd','kuma','cilium','none','unknown'))
);
CREATE INDEX idx_cluster_service_mesh_detected_mesh ON cluster_service_mesh (detected_mesh);

-- Tag rows in helm_charts as service-mesh charts so the filter on the
-- "Install" button is one query. Idempotent insert — safe to re-run.
INSERT INTO helm_chart_tags (chart_id, tag)
    SELECT id, 'service-mesh' FROM helm_charts
    WHERE name IN ('istiod','istio-base','istio-cni','istio-ingressgateway',
                   'linkerd-control-plane','linkerd-crds','linkerd2',
                   'kuma','cilium')
ON CONFLICT (chart_id, tag) DO NOTHING;
