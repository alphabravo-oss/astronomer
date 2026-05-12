-- Fleet operations (migration 056).
--
-- Coordinated multi-cluster operations. An operator picks a label
-- selector ("tier=staging"), picks an operation_type ("tool_upgrade"),
-- picks a target spec ({slug:"cert-manager", target_version:"v1.14.5"}),
-- and the orchestrator marches through every matched cluster
-- respecting max_concurrent and an on_error policy (abort / continue).
--
-- The killer use case is a rolling tool upgrade or a coordinated
-- maintenance action across 50+ clusters with bounded blast radius.
-- Per-cluster work re-uses the existing tool_operations queue: the
-- orchestrator inserts a tool_operations row per cluster as it
-- dispatches, then polls the sub-operation's status.
--
-- Two tables:
--
--   * fleet_operations         — the operator's coordinated action.
--                                Holds selector, operation_spec, run
--                                policy, aggregate counters, status.
--
--   * fleet_operation_targets — one row per matched cluster, evaluated
--                                ONCE at the pending → running
--                                transition. Subsequent ticks read this
--                                persisted list rather than re-running
--                                the selector against clusters.
--
-- Selector shape (handler-validated, stored as JSONB so future fields
-- like matchExpressions can ship without an ALTER TABLE):
--
--   {
--     matchLabels: { tier: "prod", region: "us-east" },
--     matchExpressions: [
--       { key: "region", operator: "In", values: ["us-east","us-west"] }
--     ]
--   }
--
-- Operation spec shape (type-specific; the orchestrator decodes per
-- operation_type):
--
--   tool_upgrade  / tool_install / tool_uninstall:
--     { slug: "cert-manager", target_version: "v1.14.5",
--       preset: "default", values: { ... } }
--
--   apply_template:
--     { template_id: "<uuid>" }
--
--   drain_namespaces / rotate_agent_token / custom_helm:
--     reserved for follow-up sprints. The orchestrator marks targets
--     skipped with a clear last_error when it can't dispatch.

CREATE TABLE fleet_operations (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Human-readable name. Not unique — operators may want a recurring
    -- "Weekly cert-manager refresh" naming convention without the
    -- engine refusing the second run.
    name            VARCHAR(128) NOT NULL,
    description     TEXT         NOT NULL DEFAULT '',
    -- Operation type — what to do to each matched cluster.
    --   "tool_upgrade" | "tool_install" | "tool_uninstall"
    --   | "drain_namespaces" | "apply_template"
    --   | "rotate_agent_token" | "custom_helm"
    operation_type  VARCHAR(64)  NOT NULL,
    -- JSONB carrying type-specific args. See above for per-type shapes.
    operation_spec  JSONB        NOT NULL DEFAULT '{}',
    -- Cluster selector. Same shape as Kubernetes label selectors.
    selector        JSONB        NOT NULL DEFAULT '{}',
    -- "sequential" | "parallel" — sequential runs strictly one-at-a-time;
    -- parallel runs up to max_concurrent at once.
    strategy        VARCHAR(16)  NOT NULL DEFAULT 'parallel',
    max_concurrent  INTEGER      NOT NULL DEFAULT 3,
    -- "abort" | "continue" — what to do when a cluster fails.
    on_error        VARCHAR(16)  NOT NULL DEFAULT 'abort',
    -- Optional maintenance-window gate. The orchestrator delegates to
    -- the MaintenanceWindowChecker interface; when no implementation
    -- is wired (parallel sprint), the gate is a no-op so this flag is
    -- effectively informational until the impl lands.
    respect_maintenance_windows BOOLEAN NOT NULL DEFAULT true,
    -- "pending" | "running" | "paused" | "completed" | "failed" | "aborted"
    status          VARCHAR(16)  NOT NULL DEFAULT 'pending',
    -- Aggregate counters. Updated by the orchestrator at each terminal
    -- target transition so the GET /fleet-operations/{id}/ summary
    -- doesn't have to GROUP BY the targets table on every render.
    total_clusters     INTEGER NOT NULL DEFAULT 0,
    completed_clusters INTEGER NOT NULL DEFAULT 0,
    failed_clusters    INTEGER NOT NULL DEFAULT 0,
    skipped_clusters   INTEGER NOT NULL DEFAULT 0,
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    last_error      TEXT         NOT NULL DEFAULT '',
    -- SET NULL on user delete so audit history (and the "created by"
    -- avatar in the UI) survives an admin departure. Matches the
    -- cluster_templates + projects pattern.
    created_by      UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT strategy_valid CHECK (strategy IN ('sequential','parallel')),
    CONSTRAINT on_error_valid CHECK (on_error IN ('abort','continue')),
    CONSTRAINT status_valid   CHECK (status   IN ('pending','running','paused','completed','failed','aborted'))
);

-- Partial index — the orchestrator's hot path is "give me every
-- not-yet-terminal fleet operation". Restricting the index to the
-- two non-terminal statuses keeps it tiny even after years of
-- completed runs accumulate in the table.
CREATE INDEX idx_fleet_operations_status ON fleet_operations (status)
    WHERE status IN ('pending','running');

-- Per-cluster slot inside a fleet operation. Created at launch time by
-- evaluating the selector against `clusters`. The orchestrator marches
-- through these rows respecting max_concurrent + on_error.
CREATE TABLE fleet_operation_targets (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_id    UUID         NOT NULL REFERENCES fleet_operations(id) ON DELETE CASCADE,
    cluster_id      UUID         NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    -- "pending" | "running" | "completed" | "failed" | "skipped" | "aborted"
    status          VARCHAR(16)  NOT NULL DEFAULT 'pending',
    -- The sub-operation that drove this target. For tool_upgrade /
    -- tool_install / tool_uninstall, this is the tool_operations.id
    -- the orchestrator created. NULL until the target is dispatched.
    sub_operation_id   UUID,
    sub_operation_type VARCHAR(64) NOT NULL DEFAULT '',
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    last_error      TEXT         NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- One row per (operation, cluster). Defensive — the orchestrator
    -- evaluates the selector exactly once at launch, but a duplicate
    -- INSERT (re-run of the launch step) must be caught at the DB so
    -- the operation never has two rows competing for one cluster's
    -- terminal state.
    UNIQUE (operation_id, cluster_id)
);

-- The orchestrator's "next batch to dispatch" query is
-- (operation_id = ? AND status = 'pending'); a covering index avoids
-- a sequential scan once an operation has hundreds of targets.
CREATE INDEX idx_fleet_operation_targets_op ON fleet_operation_targets (operation_id, status);
