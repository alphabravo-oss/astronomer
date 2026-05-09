-- Phase B3: Project enforcement controller.
-- Adds default LimitRange + NetworkPolicy mode to the project row, and tracks
-- per-namespace reconcile status so the UI can surface enforced/drift state.
-- A lease column is included so multiple worker pods can claim disjoint
-- namespaces during the periodic sweep without stomping each other.

ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS limit_range          JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS network_policy_mode  VARCHAR(32) NOT NULL DEFAULT 'none';

-- Sidecar table: one row per (project, cluster, namespace) so we can store
-- last_reconciled_at / last_reconcile_error and a reconcile lease without
-- reshaping the projects.namespaces JSONB blob (which the existing UI still
-- reads). The two stay in sync via the AddNamespace / RemoveNamespace
-- handlers.
CREATE TABLE IF NOT EXISTS project_namespaces (
    project_id           UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    cluster_id           UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    namespace            VARCHAR(253) NOT NULL,
    last_reconciled_at   TIMESTAMPTZ,
    last_reconcile_error TEXT NOT NULL DEFAULT '',
    -- Cooperative lease for the periodic sweep: a worker bumps locked_until
    -- to now()+30s before reconciling, other workers SKIP LOCKED past it.
    locked_until         TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, cluster_id, namespace)
);

CREATE INDEX IF NOT EXISTS idx_project_namespaces_cluster
    ON project_namespaces (cluster_id, namespace);
CREATE INDEX IF NOT EXISTS idx_project_namespaces_lease
    ON project_namespaces (locked_until);
