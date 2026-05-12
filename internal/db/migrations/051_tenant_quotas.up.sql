-- Per-tenant resource quotas (migration 051).
--
-- Rancher-style enforcement of "how much can one tenant consume" on the
-- management plane. The motivation is the same as Rancher's project /
-- project-role quotas: an operator running shared infrastructure needs to
-- prevent a single noisy tenant from eating the entire fleet's cluster
-- attach budget, opening unbounded API tokens, or flooding the SSE bus
-- with concurrent subscriptions.
--
-- Model:
--   - quota_plans is a small catalog of NAMED bundles of limits ('free',
--     'team', 'enterprise', 'global'). Each plan has an enforcement mode
--     ('hard' = reject, 'soft' = warn + allow) and a fixed set of integer
--     caps. 0 means "unlimited" — this is intentional so the 'enterprise'
--     plan's all-zeros row gives operators a one-shot "remove all caps"
--     switch and so that newly-added cap columns default to permissive.
--   - projects and users each carry a `quota_plan` FK (default 'free')
--     and a `quota_overrides` JSONB column where an operator can pin a
--     single cap to a custom value without forking the whole plan
--     (e.g. {"max_clusters_per_project": 42}).
--   - The 'global' singleton row's max_total_* fields are the fleet-wide
--     license-style caps. Only that one row's max_total_* is read by the
--     enforcer; the per-project / per-user plans' max_total_* columns
--     are kept zero by convention.
--
-- Migration safety:
--   - All ADD COLUMN lines below carry a DEFAULT on the same line, so the
--     T30 lint (check-migrations.sh) accepts them. The defaults are also
--     "permissive enough not to break existing deployments": the seeded
--     'free' plan caps at 5 clusters per project, but because the
--     migration cannot retroactively know which projects already exceed
--     that, the safe-upgrade strategy is documented in the README and
--     surfaced via a one-shot admin endpoint (operators bump high-volume
--     projects to 'enterprise' before flipping the feature on). See the
--     enforcer's CheckProject... methods for the runtime fallback.

CREATE TABLE quota_plans (
    name                        VARCHAR(64) PRIMARY KEY,
    -- "soft" = warn but allow; "hard" = reject. Default 'hard' so a new
    -- row that an operator forgot to configure errs on the safe side.
    enforcement                 VARCHAR(8) NOT NULL DEFAULT 'hard',
    description                 TEXT NOT NULL DEFAULT '',
    -- Per-project caps. 0 = unlimited.
    max_clusters_per_project    INTEGER NOT NULL DEFAULT 0,
    max_namespaces_per_project  INTEGER NOT NULL DEFAULT 0,
    max_members_per_project     INTEGER NOT NULL DEFAULT 0,
    -- Per-user caps. 0 = unlimited.
    max_projects_per_user       INTEGER NOT NULL DEFAULT 0,
    max_tokens_per_user         INTEGER NOT NULL DEFAULT 0,
    max_streams_per_user        INTEGER NOT NULL DEFAULT 0,
    -- Global (only meaningful on the 'global' plan singleton). 0 = unlimited.
    max_total_clusters          INTEGER NOT NULL DEFAULT 0,
    max_total_users             INTEGER NOT NULL DEFAULT 0,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT enforcement_valid CHECK (enforcement IN ('soft', 'hard'))
);

INSERT INTO quota_plans (
    name, enforcement, description,
    max_clusters_per_project, max_namespaces_per_project, max_members_per_project,
    max_projects_per_user, max_tokens_per_user, max_streams_per_user,
    max_total_clusters, max_total_users
) VALUES
    ('free',       'hard', 'Free tier - small footprint',                       5,   10,  10,   3,   5,  3,  0,    0),
    ('team',       'hard', 'Team tier - moderate fleet',                       20,   50,  50,  10,  20,  5,  0,    0),
    ('enterprise', 'soft', 'Enterprise tier - generous defaults, alerts only',  0,    0,   0,   0,   0,  0,  0,    0),
    ('global',     'hard', 'Singleton fleet-wide cap',                          0,    0,   0,   0,   0,  0,  0,    0)
ON CONFLICT (name) DO NOTHING;

-- Per-project / per-user override of quota plan + ad-hoc value overrides.
-- ON DELETE SET DEFAULT so removing a plan that's still referenced flips
-- the referencing rows back to 'free' rather than failing the delete (the
-- handler also rejects deletes-while-in-use as a UX guard, so this FK
-- behavior is a defense-in-depth fallback).
ALTER TABLE projects ADD COLUMN quota_plan      VARCHAR(64) NOT NULL DEFAULT 'free' REFERENCES quota_plans(name) ON DELETE SET DEFAULT;
ALTER TABLE projects ADD COLUMN quota_overrides JSONB NOT NULL DEFAULT '{}';

ALTER TABLE users    ADD COLUMN quota_plan      VARCHAR(64) NOT NULL DEFAULT 'free' REFERENCES quota_plans(name) ON DELETE SET DEFAULT;
ALTER TABLE users    ADD COLUMN quota_overrides JSONB NOT NULL DEFAULT '{}';

CREATE INDEX idx_projects_quota_plan ON projects (quota_plan);
CREATE INDEX idx_users_quota_plan    ON users    (quota_plan);
