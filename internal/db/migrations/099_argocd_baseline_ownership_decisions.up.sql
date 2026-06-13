-- Durable per-cluster baseline ownership decisions for the Argo adoption flow.

CREATE TABLE IF NOT EXISTS argocd_baseline_ownership_decisions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id     UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    component_slug TEXT NOT NULL,
    decision       TEXT NOT NULL CHECK (decision IN ('adopt', 'leave_local', 'replace')),
    reason         TEXT NOT NULL DEFAULT '',
    expires_at     TIMESTAMPTZ,
    decided_by_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, component_slug)
);

CREATE INDEX IF NOT EXISTS idx_argocd_baseline_decisions_cluster
    ON argocd_baseline_ownership_decisions (cluster_id, component_slug);
