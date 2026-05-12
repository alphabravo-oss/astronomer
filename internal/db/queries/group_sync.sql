-- Identity-group sync queries (migration 042). Drives the
-- claims-to-role-binding mapping resolved on every SSO login + the
-- admin CRUD endpoints under /api/v1/admin/group-mappings/.

-- name: CreateGroupMapping :one
-- Admin endpoint POST. Caller is responsible for validating that
-- scope/role_id/cluster_id/project_id agree before this call — the
-- table's scope_matches CHECK constraint will reject mismatched rows.
INSERT INTO identity_group_mappings (
    connector_id, group_name, scope, role_id, cluster_id, project_id
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetGroupMappingByID :one
SELECT * FROM identity_group_mappings WHERE id = $1;

-- name: ListGroupMappings :many
-- Admin endpoint GET (paginated).
SELECT * FROM identity_group_mappings
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountGroupMappings :one
SELECT count(*) FROM identity_group_mappings;

-- name: DeleteGroupMapping :exec
DELETE FROM identity_group_mappings WHERE id = $1;

-- name: ListGroupMappingsForConnector :many
-- Hot path. Used by SyncUserGroups on every successful SSO login.
-- Returns rows whose connector_id is either the supplied UUID *or*
-- NULL (the wildcard connector). The Go-side filter on group_name
-- happens in the caller so we can batch a single round-trip against
-- the user's slice — Postgres has no array-of-text-to-IN-clause
-- shortcut that sqlc can express portably.
--
-- NOTE: pgtype.UUID is the parameter type so the caller can pass an
-- Invalid (NULL) value to mean "wildcard only", though the typical
-- call has a Valid connector ID and gets both connector-scoped + NULL
-- rows back in one read.
SELECT * FROM identity_group_mappings
WHERE connector_id = $1 OR connector_id IS NULL;

-- name: UpsertUserIDPGroups :one
-- Replace the user's group snapshot on every login. The synced_at
-- timestamp drives the audit + "stale-claims" detection in the
-- admin resync endpoint.
INSERT INTO user_idp_groups (user_id, connector_id, groups, synced_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE SET
    connector_id = EXCLUDED.connector_id,
    groups       = EXCLUDED.groups,
    synced_at    = EXCLUDED.synced_at
RETURNING *;

-- name: GetUserIDPGroups :one
SELECT * FROM user_idp_groups WHERE user_id = $1;

-- name: ListGroupSyncGlobalBindings :many
-- Enumerates the user's group_sync-managed global bindings so the
-- sync loop can compute the revocation diff. Manual bindings are
-- explicitly excluded.
SELECT * FROM global_role_bindings WHERE user_id = $1 AND source = 'group_sync';

-- name: ListGroupSyncClusterBindings :many
SELECT * FROM cluster_role_bindings WHERE user_id = $1 AND source = 'group_sync';

-- name: ListGroupSyncProjectBindings :many
SELECT * FROM project_role_bindings WHERE user_id = $1 AND source = 'group_sync';

-- name: CreateGroupSyncGlobalBinding :one
-- Marks the source column with 'group_sync' on insert. ON CONFLICT
-- DO NOTHING handles the idempotent re-sync case (manual binding
-- on (user_id, role_id) wins; group_sync row stays absent).
INSERT INTO global_role_bindings (user_id, "group", role_id, source)
VALUES ($1, '', $2, 'group_sync')
ON CONFLICT (user_id, role_id) DO NOTHING
RETURNING *;

-- name: CreateGroupSyncClusterBinding :one
INSERT INTO cluster_role_bindings (user_id, "group", role_id, cluster_id, source)
VALUES ($1, '', $2, $3, 'group_sync')
ON CONFLICT (user_id, role_id, cluster_id) DO NOTHING
RETURNING *;

-- name: CreateGroupSyncProjectBinding :one
INSERT INTO project_role_bindings (user_id, "group", role_id, project_id, source)
VALUES ($1, '', $2, $3, 'group_sync')
ON CONFLICT (user_id, role_id, project_id) DO NOTHING
RETURNING *;

-- name: DeleteGroupSyncGlobalBinding :exec
-- Belt-and-suspenders source check so a sync run can never delete a
-- manual binding even if the caller picks the wrong ID.
DELETE FROM global_role_bindings WHERE id = $1 AND source = 'group_sync';

-- name: DeleteGroupSyncClusterBinding :exec
DELETE FROM cluster_role_bindings WHERE id = $1 AND source = 'group_sync';

-- name: DeleteGroupSyncProjectBinding :exec
DELETE FROM project_role_bindings WHERE id = $1 AND source = 'group_sync';

-- name: CountGroupSyncGlobalBindings :one
SELECT count(*) FROM global_role_bindings WHERE source = 'group_sync';

-- name: CountGroupSyncClusterBindings :one
SELECT count(*) FROM cluster_role_bindings WHERE source = 'group_sync';

-- name: CountGroupSyncProjectBindings :one
SELECT count(*) FROM project_role_bindings WHERE source = 'group_sync';
