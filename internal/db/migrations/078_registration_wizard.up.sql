-- Sprint 22 — Rancher-style multi-page cluster-registration wizard.
--
-- Today a cluster's lifecycle on the platform is a single flap-of-a-row:
-- POST /clusters/ writes the row, the agent eventually connects, done.
-- The frontend has no concept of "the operator is still mid-install" — a
-- refresh after the POST drops them on the cluster list with no
-- indication that anything is in-flight.
--
-- This migration adds a server-authoritative registration phase machine
-- plus a per-step audit trail used by both the wizard frontend and the
-- per-cluster "Provisioning" tab.
--
-- Design choices:
--
--   * registration_phase lives ON the clusters row (not a separate
--     "current state" table) because every cluster has exactly one
--     phase at a time and the lookup is on the hot path (every
--     dashboard list query needs it). A separate table would force a
--     JOIN on the most-trafficked read in the system.
--
--   * The per-step history goes in cluster_registration_steps. Multiple
--     rows per cluster — one per step — so the UI can render a
--     left-to-right timeline + so retries don't overwrite the original
--     failure (the operator can still see what went wrong on attempt
--     #1 after attempt #2 succeeded).
--
--   * install_baseline is nullable on purpose. NULL means "operator
--     hasn't reached step 2 of the wizard yet" — distinct from FALSE
--     ("they ticked the box off") and TRUE ("they want the baseline").
--     A boolean with a default would silently re-interpret existing
--     rows; the three-valued semantics keep migration intent explicit.

ALTER TABLE clusters ADD COLUMN IF NOT EXISTS registration_phase VARCHAR(32) NOT NULL DEFAULT 'created';
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS registration_started_at  TIMESTAMPTZ;
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS registration_completed_at TIMESTAMPTZ;
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS install_baseline BOOLEAN;

ALTER TABLE clusters DROP CONSTRAINT IF EXISTS registration_phase_valid;
ALTER TABLE clusters ADD CONSTRAINT registration_phase_valid
    CHECK (registration_phase IN ('created','awaiting_agent','connected','provisioning','ready','failed'));

-- Backfill: every row older than one minute existed before the wizard
-- shipped, so transition it to 'ready' rather than dragging it through
-- the wizard ex-post-facto. The one-minute floor leaves room for rows
-- inserted by the integration test harness during this migration run.
UPDATE clusters
SET registration_phase = 'ready',
    registration_completed_at = COALESCE(updated_at, now())
WHERE registration_phase = 'created'
  AND created_at < now() - interval '1 minute';

CREATE TABLE IF NOT EXISTS cluster_registration_steps (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    -- step_name is a stable machine-readable identifier; multiple rows
    -- per (cluster_id, step_name) tuple are allowed so retries are
    -- visible as separate timeline entries.
    step_name       VARCHAR(64) NOT NULL,
    -- label is server-rendered for UI display so the frontend doesn't
    -- need a lookup table. We compute it at insert time from step_name.
    label           VARCHAR(255) NOT NULL,
    status          VARCHAR(16) NOT NULL DEFAULT 'pending',
    -- 0-100; long-running steps (e.g. tool install) write intermediate
    -- values via PATCH so the UI can show a progress bar.
    progress_pct    INTEGER NOT NULL DEFAULT 0,
    detail_json     JSONB NOT NULL DEFAULT '{}',
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    error_message   TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    step_order      INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT step_status_valid CHECK (status IN ('pending','running','success','failed','skipped'))
);

CREATE INDEX IF NOT EXISTS idx_reg_steps_cluster
    ON cluster_registration_steps (cluster_id, step_order, created_at);
