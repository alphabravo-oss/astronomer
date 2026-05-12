-- GitOps cluster registration sources (migration 060).
--
-- Operator commits a YAML file at clusters/<name>.yaml to a tracked Git
-- repo; Astronomer polls the repo every 60s, parses each file as a
-- ClusterRegistration document (apiVersion: astronomer.alphabravo.io/v1,
-- kind: ClusterRegistration), and registers / updates the cluster via
-- the existing cluster handler create flow.
--
-- The value over click-through registration is auditable, reviewable,
-- disaster-recoverable cluster onboarding: the YAML in the repo is the
-- source of truth, and the on_delete policy (log / tombstone /
-- decommission) governs what happens when a cluster's YAML disappears.
--
-- Two tables ship with this migration:
--
--   gitops_registration_sources
--     One row per tracked repo. Stores the repo URL + branch +
--     path_prefix, auth (Fernet-encrypted blob), sync cadence, and the
--     on_delete policy. Default on_delete is 'log' (operator must
--     explicitly opt in to tombstone / decommission to avoid the blast
--     radius of an accidental rm in the repo).
--
--   gitops_registered_clusters
--     The link between a cluster row and the source that owns it.
--     repo_path tracks the file path so a rename in the repo updates
--     this row's path. last_yaml_sha gates no-op convergence: matching
--     sha = nothing to apply. tombstoned_at records when the YAML
--     disappeared under on_delete='tombstone'; the reaper fires
--     cluster:decommission after a 24h grace if the file doesn't come
--     back.
--
-- Migration safety:
--   - Every NOT NULL has a DEFAULT on the same line so check-migrations.sh
--     stays clean.
--   - ON DELETE CASCADE on the cluster_id FK so deleting a cluster also
--     drops its gitops link (the cluster handler's delete path is the
--     authoritative deregister).
--   - ON DELETE CASCADE on the source_id FK so deleting a source drops
--     all its tracked-cluster rows. The clusters themselves remain; the
--     operator can re-register them under a different source or via
--     the UI.
--   - ON DELETE SET NULL on created_by so deleting a user doesn't take
--     the audit trail with it.
--
-- Defaults align with the v1 conservative posture:
--   - branch        = 'main'
--   - path_prefix   = ''        (entire repo from root)
--   - auth_mode     = 'none'    (public repo or read-only mirror)
--   - sync_mode     = 'interval'
--   - sync_interval = 60s
--   - on_delete     = 'log'     (alert only, operator handles deregister)
--   - enabled       = true

CREATE TABLE gitops_registration_sources (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(128) NOT NULL UNIQUE,
    -- Git repo URL: https or ssh
    repo_url        TEXT NOT NULL,
    -- Default "main". Operators may track a release branch.
    branch          VARCHAR(64) NOT NULL DEFAULT 'main',
    -- Subpath inside the repo. Default "" = repo root. Files at depth 1+
    -- below this path are processed.
    path_prefix     VARCHAR(256) NOT NULL DEFAULT '',
    -- Auth mode + Fernet-encrypted credential blob.
    -- "none" | "https_token" | "ssh_key"
    auth_mode       VARCHAR(16) NOT NULL DEFAULT 'none',
    auth_encrypted  TEXT NOT NULL DEFAULT '',
    -- "manual" = operator clicks "Sync now"; "interval" = poller at sync_interval_seconds.
    sync_mode       VARCHAR(16) NOT NULL DEFAULT 'interval',
    sync_interval_seconds INTEGER NOT NULL DEFAULT 60,
    -- When the YAML for a cluster disappears from the repo, default behavior:
    --   "log" — flag in audit log, take no action (operator manually deregisters)
    --   "tombstone" — mark cluster as decommissioning + 24h grace; if YAML
    --       returns, undo. After 24h with no YAML, fire the decommission task.
    --   "decommission" — immediate decommission (DANGEROUS; opt-in)
    on_delete       VARCHAR(16) NOT NULL DEFAULT 'log',
    -- Last successful sync metadata
    last_synced_at  TIMESTAMPTZ,
    last_synced_sha VARCHAR(64) NOT NULL DEFAULT '',
    last_error      TEXT NOT NULL DEFAULT '',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT auth_mode_valid CHECK (auth_mode IN ('none','https_token','ssh_key')),
    CONSTRAINT sync_mode_valid CHECK (sync_mode IN ('manual','interval')),
    CONSTRAINT on_delete_valid CHECK (on_delete IN ('log','tombstone','decommission'))
);
CREATE INDEX idx_gitops_registration_sources_enabled ON gitops_registration_sources (enabled);

-- Tracks every cluster that came from a gitops source. The link is by name
-- (metadata.name in the YAML matches cluster.name). The source-of-truth file
-- path is also recorded so a rename in the repo flips this row.
CREATE TABLE gitops_registered_clusters (
    cluster_id      UUID PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
    source_id       UUID NOT NULL REFERENCES gitops_registration_sources(id) ON DELETE CASCADE,
    repo_path       TEXT NOT NULL,
    last_yaml_sha   VARCHAR(64) NOT NULL DEFAULT '',
    last_applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- "active" | "tombstoned" — set to tombstoned when YAML disappears under
    -- on_delete=tombstone. The decommission task fires after grace.
    status          VARCHAR(16) NOT NULL DEFAULT 'active',
    tombstoned_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT status_valid CHECK (status IN ('active','tombstoned'))
);
CREATE INDEX idx_gitops_registered_clusters_source ON gitops_registered_clusters (source_id);
CREATE INDEX idx_gitops_tombstoned_clusters ON gitops_registered_clusters (tombstoned_at) WHERE status = 'tombstoned';
