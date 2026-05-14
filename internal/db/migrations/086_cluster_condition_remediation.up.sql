-- Sprint 086 — cluster-condition remediation attempts.
--
-- The cluster_conditions table (migration 035) records *that* a
-- condition flipped to False — Connected, AgentReachable,
-- GatewayAPISupported, etc. Until now nothing acted on those rows;
-- the on-call operator saw a red pill and had to remediate by hand.
--
-- This migration adds a write-side audit trail for the remediation
-- controller (internal/worker/tasks/cluster_condition_reconcile.go).
-- Every attempt to remedy a False condition lands here:
--
--   - `action` is the controller's chosen remedy (machine-readable
--     enum so future UIs can render icons consistently).
--   - `outcome` is success | failed | skipped (the last for the
--     "we're within backoff, ignoring this tick" branch).
--   - `error` is the upstream error string when outcome=failed.
--
-- The controller reads back the latest row per (cluster_id,
-- condition_type) to compute exponential backoff so a stuck cluster
-- doesn't tight-loop.
--
-- Design choices:
--
--   * `cluster_condition_remediation_attempts` rather than appending
--     to cluster_conditions because attempts are append-only history,
--     not current state — and conditions get UPSERTed on every probe
--     tick, which would lose attempt history.
--
--   * Two indexes: (cluster_id, condition_type, attempted_at DESC) so
--     the "latest attempt for backoff lookup" query is index-only,
--     and (attempted_at) so the retention sweep can find old rows
--     without a sequential scan.
--
--   * No FK to cluster_conditions(id) because the conditions row's
--     UPSERT-on-probe behavior means its id is unstable (deleted +
--     re-created when status changes through Unknown). We keep
--     cluster_id + type as the join key, the same shape the
--     controller queries on.
--
--   * `outcome VARCHAR(16)` rather than ENUM because the controller's
--     vocabulary may grow ("rate_limited", "permission_denied" etc.)
--     and ENUMs are PITA to extend on Postgres without a maintenance
--     window. VARCHAR + CHECK gives us migration ergonomics without
--     losing the lint.

CREATE TABLE cluster_condition_remediation_attempts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    condition_type  VARCHAR(64) NOT NULL,
    -- Action taken by the controller. Examples shipped at first cut:
    --   "registration_token_reissued"
    --   "agent_version_recommendation_recorded"
    --   "cert_manager_reapply_enqueued"
    --   "noop_in_backoff"
    action          VARCHAR(64) NOT NULL,
    outcome         VARCHAR(16) NOT NULL CHECK (outcome IN ('success', 'failed', 'skipped')),
    -- Free-form detail. Populated on outcome=failed with the upstream
    -- error string; otherwise empty.
    error           TEXT        NOT NULL DEFAULT '',
    -- Detail JSONB so the controller can stash whatever the UI / audit
    -- log needs (e.g. {"token_id": "...", "expires_at": "..."}).
    detail          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    attempted_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backoff lookup hits this index — latest attempt per (cluster, type).
CREATE INDEX idx_ccra_cluster_type_attempted
    ON cluster_condition_remediation_attempts (cluster_id, condition_type, attempted_at DESC);

-- Retention sweep — daily prune of rows older than 30d (handler TBD)
-- joins through this. Cheap to add now.
CREATE INDEX idx_ccra_attempted_at
    ON cluster_condition_remediation_attempts (attempted_at);
