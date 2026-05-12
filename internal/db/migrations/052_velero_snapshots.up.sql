-- Per-cluster Velero-driven snapshot + restore self-service (migration 052).
--
-- Astronomer's "management plane" Velero integration (Phase B2) already
-- ships a `BackupStorageConfig` row + a `Backup` row per pg_dump-style
-- run against the control plane. That path is operator-internal and
-- only mirrors a small set of CRDs into the control-plane's own
-- velero namespace.
--
-- This migration adds the FLEET-FACING surface: per-member-cluster
-- Velero backup + restore + scheduled-snapshot rows. An operator
-- registers Velero in a member cluster (via the helm catalog) and then
-- drives Backup / Restore / Schedule CRDs through Astronomer's API:
--
--   - cluster_snapshots          — one row per Velero Backup CRD
--                                  (manual or fired by a schedule). The
--                                  poller mirrors Velero's BackupStatus.
--   - cluster_restores           — one row per Velero Restore CRD,
--                                  joined to the snapshot it consumed.
--                                  target_cluster_id may differ from
--                                  the snapshot's cluster (cross-cluster
--                                  restore) — see the handler's pre-
--                                  flight check.
--   - cluster_snapshot_schedules — cron-driven Velero Backup creation,
--                                  evaluated by the dispatcher worker
--                                  every minute.
--
-- Velero itself stores the actual snapshot payload in its configured
-- BackupStorageLocation (S3 / Azure / GCS) — these tables are the
-- index + status mirror so the Astronomer UI can list/filter/restore
-- without round-tripping to every member cluster on every page load.

CREATE TABLE cluster_snapshots (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    -- Velero Backup CRD name in the target cluster's velero namespace.
    velero_name     VARCHAR(253) NOT NULL,
    velero_namespace VARCHAR(63)  NOT NULL DEFAULT 'velero',
    -- "manual" | "scheduled" | "template_apply" — provenance.
    source          VARCHAR(32) NOT NULL DEFAULT 'manual',
    -- Spec the user gave us; mirrored to the Velero CRD.
    spec            JSONB NOT NULL DEFAULT '{}',
    -- Last status copy from Velero. Updated by the poller.
    phase           VARCHAR(32) NOT NULL DEFAULT 'New',
    start_time      TIMESTAMPTZ,
    completion_time TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ,
    -- Velero's "warnings" + "errors" counters from BackupStatus.
    warnings_count  INTEGER NOT NULL DEFAULT 0,
    errors_count    INTEGER NOT NULL DEFAULT 0,
    last_poll_at    TIMESTAMPTZ,
    last_poll_error TEXT NOT NULL DEFAULT '',
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_cluster_snapshots_cluster ON cluster_snapshots (cluster_id, created_at DESC);
CREATE INDEX idx_cluster_snapshots_phase   ON cluster_snapshots (phase) WHERE phase IN ('InProgress','New');

-- Restores from a snapshot. Tracks the Velero Restore CRD lifecycle.
CREATE TABLE cluster_restores (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    snapshot_id     UUID NOT NULL REFERENCES cluster_snapshots(id) ON DELETE CASCADE,
    -- Could be a different cluster than the snapshot's cluster — cross-cluster restore.
    target_cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    velero_name     VARCHAR(253) NOT NULL,
    velero_namespace VARCHAR(63) NOT NULL DEFAULT 'velero',
    spec            JSONB NOT NULL DEFAULT '{}',
    phase           VARCHAR(32) NOT NULL DEFAULT 'New',
    start_time      TIMESTAMPTZ,
    completion_time TIMESTAMPTZ,
    warnings_count  INTEGER NOT NULL DEFAULT 0,
    errors_count    INTEGER NOT NULL DEFAULT 0,
    last_poll_at    TIMESTAMPTZ,
    last_poll_error TEXT NOT NULL DEFAULT '',
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_cluster_restores_cluster ON cluster_restores (target_cluster_id, created_at DESC);
CREATE INDEX idx_cluster_restores_phase   ON cluster_restores (phase) WHERE phase IN ('InProgress','New');

-- Scheduled snapshots: cron-driven Velero Backup creation. Each row = one schedule.
-- We compute next-run-at in code via robfig/cron/v3; the row only tracks
-- last_run_at so a paused-then-resumed schedule fires immediately on the
-- next dispatcher tick rather than waiting a full cycle.
CREATE TABLE cluster_snapshot_schedules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    name            VARCHAR(128) NOT NULL,
    cron_schedule   VARCHAR(64)  NOT NULL,        -- standard 5-field cron
    spec            JSONB NOT NULL DEFAULT '{}',  -- includedNamespaces, ttl, etc.
    enabled         BOOLEAN NOT NULL DEFAULT true,
    last_run_at     TIMESTAMPTZ,
    last_run_status VARCHAR(32) NOT NULL DEFAULT '',
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);
CREATE INDEX idx_cluster_snapshot_schedules_cluster ON cluster_snapshot_schedules (cluster_id);
CREATE INDEX idx_cluster_snapshot_schedules_enabled ON cluster_snapshot_schedules (enabled) WHERE enabled = true;
