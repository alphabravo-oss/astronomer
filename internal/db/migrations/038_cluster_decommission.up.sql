-- Phase: cluster decommission reconciler.
--
-- Previously, clicking "Remove cluster" in the UI hard-deleted the cluster row
-- via ON DELETE CASCADE, leaving residue: agent WS tunnels timed out instead
-- of being severed, managed-side Helm releases / DaemonSets stayed running,
-- audit_log rows referencing the cluster floated, and registration tokens
-- were not revoked. This migration introduces:
--
--   1. `cluster_decommissions` — a controller-reconciled work queue. The UI
--      DELETE now enqueues a decommission instead of hard-deleting; the
--      worker walks phases (token revoke, managed-side cleanup, audit
--      archive, dependent cleanup, tombstone) and records the outcome per
--      phase in the `phases` JSONB blob.
--
--   2. `audit_archive` — archived audit rows. We never want to lose the
--      action history of a removed cluster; before tombstoning we copy rows
--      from `audit_log` to this table (which is NOT partitioned and not
--      subject to retention) and DELETE from `audit_log`. The schema mirrors
--      audit_log so re-hydration is a column-for-column INSERT … SELECT.
--
--   3. `decommissioned_at` on `clusters` — the soft-delete tombstone marker.
--      The cluster row stays around (so audit_archive.resource_id remains
--      meaningful) but the row is excluded from List by default.

CREATE TABLE IF NOT EXISTS cluster_decommissions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Reference is intentionally NOT a FK. The cluster row is soft-deleted
    -- (decommissioned_at IS NOT NULL) at the end of the reconciler so the id
    -- remains in clusters for the lifetime of the audit archive; should the
    -- cluster row ever be hard-deleted manually, decommission rows survive
    -- with the cluster_id remembered for forensics.
    cluster_id   UUID NOT NULL,
    status       VARCHAR(16) NOT NULL DEFAULT 'pending',
    -- phases is a JSON object keyed by phase name (revoke_agent_token,
    -- cleanup_managed_side, archive_audit, delete_dependents, tombstone_cluster)
    -- whose value is { "status": "...", "started_at": "...", "completed_at":
    -- "...", "error": "..." }. The worker writes each entry atomically as it
    -- progresses; idempotent re-runs skip phases whose status == "succeeded".
    phases       JSONB NOT NULL DEFAULT '{}',
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error   TEXT NOT NULL DEFAULT '',
    attempts     INTEGER NOT NULL DEFAULT 0,
    -- Forensic columns: who initiated and what was the cluster's friendly
    -- name at the time of decommission. Captured at enqueue time because the
    -- cluster row will be soft-deleted by the reconciler.
    requested_by_id UUID REFERENCES users(id) ON DELETE SET NULL,
    cluster_name TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_cluster_decommissions_cluster
    ON cluster_decommissions (cluster_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_cluster_decommissions_status
    ON cluster_decommissions (status, created_at);

-- The cluster soft-delete tombstone. NULL means "live", non-NULL means
-- the decommission reconciler reached the final phase.
ALTER TABLE clusters
    ADD COLUMN IF NOT EXISTS decommissioned_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_clusters_decommissioned_at
    ON clusters (decommissioned_at);

-- audit_archive: column-for-column mirror of audit_log so the
-- archive_audit phase is a pure copy. Not partitioned: archived rows are
-- read-rarely; the volume per decommissioned cluster is bounded.
CREATE TABLE IF NOT EXISTS audit_archive (
    id                  UUID NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL,
    schema_version      VARCHAR(32) NOT NULL DEFAULT 'audit-v1',
    user_id             UUID,
    actor_auth_method   VARCHAR(32) NOT NULL DEFAULT '',
    action              VARCHAR(64) NOT NULL,
    resource_type       VARCHAR(64) NOT NULL,
    resource_id         VARCHAR(255) NOT NULL DEFAULT '',
    resource_name       VARCHAR(255) NOT NULL DEFAULT '',
    http_method         VARCHAR(16) NOT NULL DEFAULT '',
    path                TEXT NOT NULL DEFAULT '',
    status_code         INTEGER NOT NULL DEFAULT 0,
    duration_ms         BIGINT NOT NULL DEFAULT 0,
    request_id          VARCHAR(64) NOT NULL DEFAULT '',
    ip_address          INET,
    user_agent          TEXT NOT NULL DEFAULT '',
    detail              JSONB NOT NULL DEFAULT '{}',
    source              VARCHAR(16) NOT NULL DEFAULT 'service',
    correlation_id      VARCHAR(64) NOT NULL DEFAULT '',
    -- archived_cluster_id is the cluster the row was tied to (resource_id
    -- when resource_type='cluster', or extracted from detail.cluster_id
    -- otherwise). Indexed so a cluster's full history can be replayed.
    archived_cluster_id UUID,
    archived_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
);

CREATE INDEX IF NOT EXISTS idx_audit_archive_cluster
    ON audit_archive (archived_cluster_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_archive_resource
    ON audit_archive (resource_type, resource_id);

CREATE INDEX IF NOT EXISTS idx_audit_archive_archived_at
    ON audit_archive (archived_at DESC);
