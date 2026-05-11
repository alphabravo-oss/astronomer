-- Kubernetes-style per-cluster conditions.
--
-- The existing cluster_health_statuses.conditions JSONB is a single
-- {connected, last_heartbeat, ...} snapshot — useful for the topbar but
-- can't express "the AgentReachable condition has been False since X".
-- This table follows the metav1.Condition shape so the UI can render
-- pills exactly like kubectl does.
--
-- Authoring is done by the periodic cluster:health_check task, which
-- upserts one row per (cluster_id, type). Reading is a single query per
-- cluster.

CREATE TABLE cluster_conditions (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id            UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    -- Condition type, e.g. "Connected", "AgentReachable", "GatewayAPISupported".
    -- VARCHAR(64) is a deliberate cap so frontends can switch-case safely.
    type                  VARCHAR(64) NOT NULL,
    -- Tri-state matching metav1.ConditionStatus: "True"/"False"/"Unknown".
    status                VARCHAR(8)  NOT NULL,
    -- Machine-readable enum, e.g. "AgentDisconnected", "APITimeout".
    reason                VARCHAR(64) NOT NULL DEFAULT '',
    -- Human-readable, often shown in tooltip / detail.
    message               TEXT        NOT NULL DEFAULT '',
    -- Updated only when `status` actually changes — clients use this to
    -- show "Programmed for 3h".
    last_transition_time  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Refreshed on every probe regardless of status change.
    last_probe_time       TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, type)
);
CREATE INDEX idx_cluster_conditions_cluster_id ON cluster_conditions (cluster_id);
CREATE INDEX idx_cluster_conditions_type_status ON cluster_conditions (type, status);
