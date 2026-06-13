-- Durable control-plane intents for adopted-cluster agent lifecycle actions.
--
-- The agent fleet UI can already compute an upgrade plan. This table records
-- an operator-approved intent so a reconciler or future agent-side executor can
-- pick it up, show history, and audit who queued it.

CREATE TABLE IF NOT EXISTS agent_lifecycle_operations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id        UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    operation_type    TEXT NOT NULL
        CHECK (operation_type IN ('agent_upgrade')),
    status            TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled')),
    target_version    TEXT NOT NULL,
    target_image      TEXT NOT NULL,
    current_version   TEXT NOT NULL DEFAULT '',
    strategy          TEXT NOT NULL DEFAULT 'manifest_rollout',
    operation_spec    JSONB NOT NULL DEFAULT '{}'::jsonb,
    requested_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    started_at        TIMESTAMPTZ,
    completed_at      TIMESTAMPTZ,
    last_error        TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_agent_lifecycle_operations_cluster_created
    ON agent_lifecycle_operations (cluster_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_lifecycle_operations_status
    ON agent_lifecycle_operations (status, created_at ASC);
