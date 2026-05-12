-- Cluster apiserver allow-list (migration 070).
--
-- Sprint 070 — "lockbox" mode for the apiserver of every managed cluster.
--
-- Operators register a small CIDR list (their VPN / bastion / monitoring
-- IPs) and pick a mode:
--   - "monitor"  : the reconciler only records drift; never writes.
--   - "enforce"  : the reconciler patches the cloud-LB / firewall on
--                  every divergence between desired + effective.
--   - "disabled" : no reconciliation, no monitoring.
--
-- The renderer always ADDS the Astronomer tunnel egress block on top of
-- the operator-defined CIDRs before producing the desired state. Operators
-- cannot remove the egress block (a delete would brick the tunnel) — it's
-- stamped into the desired set every reconcile.
--
-- The detected_provider column is filled in by the reconciler from the
-- cluster's annotations / labels (EKS / GKE / AKS / DOKS) or falls back
-- to "self_managed" for kubeadm / k3s / RKE. v1 only auto-enforces on
-- the four cloud-managed providers; self_managed enforcement is operator-
-- driven (they tell us the kube-system NetworkPolicy name to patch).
--
-- Migration safety:
--   - Every NOT NULL has a DEFAULT on the same line (check-migrations.sh
--     T30 lint requirement). Operators upgrading carry zero rows in
--     either new table so the defaults never run against populated data.
--   - ON DELETE CASCADE on the cluster_id FK pair so removing a cluster
--     cleanly removes its allow-list config + history. The cloud-side
--     patch is left in place by design — operator-rotation tooling owns
--     the final unbinding step.
--   - Snapshot retention is 90 days, enforced by the daily cleanup task
--     (apiserver_allowlist:cleanup_snapshots).

CREATE TABLE apiserver_allowlists (
    cluster_id      UUID PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
    -- Operator-defined CIDR list. Astronomer's tunnel egress IPs are
    -- ADDED on top in the renderer; they don't live in the DB so they
    -- can change over time (e.g. when the tunnel egress moves).
    cidrs           JSONB NOT NULL DEFAULT '[]',
    -- "enforce" | "monitor" | "disabled"
    --   enforce : the reconciler actively patches the cloud-LB / firewall
    --   monitor : the reconciler only records drift; never writes
    --   disabled: no reconciliation, no monitoring
    mode            VARCHAR(16) NOT NULL DEFAULT 'monitor',
    -- Detected provider: 'eks' | 'gke' | 'aks' | 'doks' | 'self_managed' | 'unknown'
    -- Set by the reconciler; defaults to 'unknown' until first reconcile.
    detected_provider VARCHAR(32) NOT NULL DEFAULT 'unknown',
    -- Last reconcile state.
    last_reconciled_at TIMESTAMPTZ,
    -- "synced" | "drifting" | "pending" | "failed"
    sync_status      VARCHAR(16) NOT NULL DEFAULT 'pending',
    last_error       TEXT NOT NULL DEFAULT '',
    -- Snapshot of the effective in-cluster allow-list at last reconcile.
    -- JSONB array of CIDR strings.
    effective_cidrs  JSONB NOT NULL DEFAULT '[]',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT allowlist_mode_valid CHECK (mode IN ('enforce','monitor','disabled')),
    CONSTRAINT allowlist_status_valid CHECK (sync_status IN ('synced','drifting','pending','failed'))
);

-- Per-cluster history of effective-list snapshots — useful for audit and
-- "what changed in our access posture" investigations. Retention 90d,
-- enforced by the daily cleanup task.
CREATE TABLE apiserver_allowlist_snapshots (
    id              BIGSERIAL PRIMARY KEY,
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    captured_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    effective_cidrs JSONB NOT NULL,
    desired_cidrs   JSONB NOT NULL,
    drift           BOOLEAN NOT NULL DEFAULT false
);
CREATE INDEX idx_allowlist_snapshots_cluster ON apiserver_allowlist_snapshots (cluster_id, captured_at DESC);
