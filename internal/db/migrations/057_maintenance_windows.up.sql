-- Maintenance windows (migration 057).
--
-- Operator-defined time windows that gate destructive mutations across
-- the management plane. The use case is change-management 101: an
-- operator declares "no destructive ops between 9am-5pm UTC Monday
-- through Friday on tier=prod clusters" and Astronomer either refuses
-- the request (409 maintenance_window_active) or defers it (202 +
-- deferred_id) until the window opens again.
--
-- Two tables ship with this migration:
--
--   maintenance_windows
--     One row per operator-managed window. cron_open + duration_minutes
--     defines when the window is "active"; mode chooses whether being
--     active blocks or permits ops. cluster_selector + operation_types
--     scope the window. on_block picks between 409 and 202.
--
--   deferred_operations
--     Per-operation row created when a handler decides to defer rather
--     than refuse. The maintenance:dispatch_deferred task drains these
--     every 60s by re-firing the original operation through the same
--     handler/task pipeline.
--
-- Default chart values ship zero rows in either table — windows are
-- strictly opt-in. The handler-side evaluator is also nil-safe (zero
-- enabled windows = no check overhead beyond the cache hit).
--
-- Migration safety:
--   - Every NOT NULL has a DEFAULT on the same line so
--     check-migrations.sh's T30 lint stays clean.
--   - ON DELETE CASCADE on deferred_operations.window_id so deleting a
--     window cleanly drops the queued ops; the operator who deletes the
--     window is implicitly cancelling those deferrals.
--   - ON DELETE SET NULL on created_by + requested_by since deleting a
--     user shouldn't take the audit trail with it.

CREATE TABLE maintenance_windows (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(128) NOT NULL UNIQUE,
    description     TEXT NOT NULL DEFAULT '',
    -- Time-window semantics:
    --   "blackout"  = destructive ops REFUSED during the window (default)
    --   "permitted" = destructive ops ONLY ALLOWED during the window
    -- Default operator stance: blackout windows during business hours.
    mode            VARCHAR(16) NOT NULL DEFAULT 'blackout',
    -- Cron expression for window OPEN times (5-field standard cron).
    -- e.g. "0 9 * * 1-5" = 9am Monday-Friday
    cron_open       VARCHAR(64) NOT NULL,
    -- Window DURATION in minutes from open time.
    duration_minutes INTEGER NOT NULL DEFAULT 60,
    -- IANA timezone, e.g. "America/New_York" or "UTC".
    timezone        VARCHAR(64) NOT NULL DEFAULT 'UTC',
    -- Cluster scope: empty selector = all clusters; otherwise the same
    -- label-selector shape as fleet operations. Operations targeting a
    -- specific cluster check this against the cluster's labels.
    cluster_selector JSONB NOT NULL DEFAULT '{}',
    -- Operation types gated. Empty = all destructive ops. Specific:
    --   ["cluster.delete", "helm.upgrade", "project.delete"]
    operation_types JSONB NOT NULL DEFAULT '[]',
    -- "refuse" = return 409
    -- "defer"  = queue the operation to run at next window open
    on_block        VARCHAR(16) NOT NULL DEFAULT 'refuse',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT mode_valid CHECK (mode IN ('blackout','permitted')),
    CONSTRAINT on_block_valid CHECK (on_block IN ('refuse','defer'))
);
CREATE INDEX idx_maintenance_windows_enabled ON maintenance_windows (enabled);

-- Operations that were deferred. The deferred-ops worker checks every
-- minute whether the relevant window is open and dispatches them.
CREATE TABLE deferred_operations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    window_id       UUID NOT NULL REFERENCES maintenance_windows(id) ON DELETE CASCADE,
    operation_type  VARCHAR(64) NOT NULL,
    operation_spec  JSONB NOT NULL DEFAULT '{}',
    target_cluster_id UUID REFERENCES clusters(id) ON DELETE CASCADE,
    target_project_id UUID REFERENCES projects(id) ON DELETE CASCADE,
    -- "pending" | "dispatched" | "expired" | "cancelled"
    status          VARCHAR(16) NOT NULL DEFAULT 'pending',
    deferred_until  TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ,
    requested_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    last_error      TEXT NOT NULL DEFAULT '',
    dispatched_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_deferred_operations_pending ON deferred_operations (status, deferred_until) WHERE status = 'pending';
CREATE INDEX idx_deferred_operations_window ON deferred_operations (window_id);
