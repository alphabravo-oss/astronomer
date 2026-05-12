-- Group-claim sync: maps an external identity-provider group name to one
-- or more Astronomer RBAC role bindings. Resolved on every SSO login —
-- every user whose claims include this group gets these bindings created
-- and reconciled (claims-derived bindings get source='group_sync' so
-- subsequent syncs can revoke them when the group is gone).
--
-- The companion table user_idp_groups holds the most recently observed
-- groups slice per user, separately from `users` so we don't bloat the
-- main row on every login (it's audited + the admin "resync-groups"
-- endpoint reads it for the no-fresh-claims path).
--
-- All ALTER COLUMN additions are NOT NULL DEFAULT to keep the migration
-- non-blocking on a populated table (T30 lint).

CREATE TABLE identity_group_mappings (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- IdP source: which Dex connector this group came from. NULL = any
    -- connector (wildcard mapping). Allows operators to scope
    -- "github org X" separately from "Okta group Y" even if both happen
    -- to be named "engineers".
    connector_id UUID REFERENCES dex_connectors(id) ON DELETE CASCADE,
    -- The group string as it appears in the claims. TEXT, not VARCHAR —
    -- enterprise group strings can be LDAP DNs (CN=engineering,OU=teams)
    -- or SCIM URIs that comfortably exceed 255 chars.
    group_name   TEXT NOT NULL,
    -- Target binding scope.
    scope        VARCHAR(16) NOT NULL,
    -- References the right *_roles table per scope. Validated in
    -- application code rather than via FK because Postgres has no
    -- discriminated-union FK; the wrong-scope insert would just fail
    -- the *_role_bindings FK at sync time anyway.
    role_id      UUID NOT NULL,
    cluster_id   UUID REFERENCES clusters(id) ON DELETE CASCADE,
    project_id   UUID REFERENCES projects(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT scope_valid CHECK (scope IN ('global','cluster','project')),
    CONSTRAINT scope_matches CHECK (
        (scope='global'  AND cluster_id IS NULL AND project_id IS NULL) OR
        (scope='cluster' AND cluster_id IS NOT NULL AND project_id IS NULL) OR
        (scope='project' AND cluster_id IS NULL AND project_id IS NOT NULL)
    )
);

-- Partial uniqueness over the discriminated tuple. COALESCE-on-NULL lets
-- the unique index actually deduplicate wildcard-connector / global-scope
-- rows (NULL != NULL otherwise).
CREATE UNIQUE INDEX uidx_group_map_unique
    ON identity_group_mappings (
        COALESCE(connector_id::text, ''),
        group_name,
        scope,
        role_id,
        COALESCE(cluster_id::text, ''),
        COALESCE(project_id::text, '')
    );

-- Hot path: the login flow looks up every mapping that matches a user's
-- claimed group names for a given connector. Connector-aware index
-- catches both connector-scoped and (via the wildcard NULL row scan)
-- the union we read.
CREATE INDEX idx_group_map_lookup
    ON identity_group_mappings (connector_id, group_name);

-- User -> last-seen groups snapshot. Used for audit + the admin
-- "resync-groups" endpoint where the call doesn't have fresh claims.
CREATE TABLE user_idp_groups (
    user_id      UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    connector_id UUID REFERENCES dex_connectors(id) ON DELETE SET NULL,
    groups       JSONB NOT NULL DEFAULT '[]',
    synced_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Provenance on every *_role_bindings row. 'manual' (default) is never
-- touched by sync — preserves existing bindings on first deploy.
-- 'group_sync' is reconciled on every login: created when a matching
-- mapping appears in the user's claims, deleted when it no longer does.
ALTER TABLE global_role_bindings  ADD COLUMN source VARCHAR(32) NOT NULL DEFAULT 'manual';
ALTER TABLE cluster_role_bindings ADD COLUMN source VARCHAR(32) NOT NULL DEFAULT 'manual';
ALTER TABLE project_role_bindings ADD COLUMN source VARCHAR(32) NOT NULL DEFAULT 'manual';

-- The sync loop needs to enumerate every group-sync binding for a user
-- so it can compute the diff against the current mapping set. Indexed
-- partial scan keeps it cheap even when manual bindings dominate.
CREATE INDEX idx_grb_group_sync ON global_role_bindings (user_id) WHERE source = 'group_sync';
CREATE INDEX idx_crb_group_sync ON cluster_role_bindings (user_id) WHERE source = 'group_sync';
CREATE INDEX idx_prb_group_sync ON project_role_bindings (user_id) WHERE source = 'group_sync';
