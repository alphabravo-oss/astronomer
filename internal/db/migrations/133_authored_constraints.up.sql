-- P-04 Custom Gatekeeper constraint authoring.
--
-- Persists operator-authored ConstraintTemplates / Constraints that were
-- validated and server-side-applied to a cluster through the agent tunnel. The
-- embedded bundle (internal/gatekeeperpolicy) remains the source of truth for
-- the starter policy set; this table only records the CUSTOM resources authored
-- via the API so they can be listed and removed later.
--
-- (cluster_id, name) is unique: applying an authored constraint with the same
-- name re-applies (upsert), it does not create a duplicate row.
CREATE TABLE authored_constraints (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    api_version TEXT NOT NULL,
    yaml TEXT NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

CREATE INDEX idx_authored_constraints_cluster
    ON authored_constraints (cluster_id);
