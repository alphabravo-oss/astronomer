-- Global Roles

-- name: GetGlobalRoleByID :one
SELECT * FROM global_roles WHERE id = $1;

-- name: ListGlobalRoles :many
SELECT * FROM global_roles ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreateGlobalRole :one
INSERT INTO global_roles (name, display_name, description, permissions, rules, is_builtin)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateGlobalRole :one
UPDATE global_roles SET
    name = $2,
    display_name = $3,
    description = $4,
    permissions = $5,
    rules = $6
WHERE id = $1
RETURNING *;

-- name: DeleteGlobalRole :exec
DELETE FROM global_roles WHERE id = $1;

-- name: CountGlobalRoles :one
SELECT count(*) FROM global_roles;

-- Global Role Bindings

-- name: GetGlobalRoleBindingByID :one
SELECT * FROM global_role_bindings WHERE id = $1;

-- name: ListGlobalRoleBindings :many
SELECT * FROM global_role_bindings ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreateGlobalRoleBinding :one
INSERT INTO global_role_bindings (user_id, "group", role_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: DeleteGlobalRoleBinding :exec
DELETE FROM global_role_bindings WHERE id = $1;

-- Cluster Roles

-- name: GetClusterRoleByID :one
SELECT * FROM cluster_roles WHERE id = $1;

-- name: ListClusterRoles :many
SELECT * FROM cluster_roles ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreateClusterRole :one
INSERT INTO cluster_roles (name, display_name, description, permissions, rules, is_builtin)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateClusterRole :one
UPDATE cluster_roles SET
    name = $2,
    display_name = $3,
    description = $4,
    permissions = $5,
    rules = $6
WHERE id = $1
RETURNING *;

-- name: DeleteClusterRole :exec
DELETE FROM cluster_roles WHERE id = $1;

-- name: CountClusterRoles :one
SELECT count(*) FROM cluster_roles;

-- Cluster Role Bindings

-- name: GetClusterRoleBindingByID :one
SELECT * FROM cluster_role_bindings WHERE id = $1;

-- name: ListClusterRoleBindings :many
SELECT * FROM cluster_role_bindings ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListClusterRoleBindingsByCluster :many
SELECT * FROM cluster_role_bindings WHERE cluster_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: CreateClusterRoleBinding :one
INSERT INTO cluster_role_bindings (user_id, "group", role_id, cluster_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: DeleteClusterRoleBinding :exec
DELETE FROM cluster_role_bindings WHERE id = $1;

-- Project Roles

-- name: GetProjectRoleByID :one
SELECT * FROM project_roles WHERE id = $1;

-- name: ListProjectRoles :many
SELECT * FROM project_roles ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreateProjectRole :one
INSERT INTO project_roles (name, display_name, description, permissions, rules, is_builtin)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateProjectRole :one
UPDATE project_roles SET
    name = $2,
    display_name = $3,
    description = $4,
    permissions = $5,
    rules = $6
WHERE id = $1
RETURNING *;

-- name: DeleteProjectRole :exec
DELETE FROM project_roles WHERE id = $1;

-- name: CountProjectRoles :one
SELECT count(*) FROM project_roles;

-- Project Role Bindings

-- name: GetProjectRoleBindingByID :one
SELECT * FROM project_role_bindings WHERE id = $1;

-- name: ListProjectRoleBindings :many
SELECT * FROM project_role_bindings ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListProjectRoleBindingsByProject :many
SELECT * FROM project_role_bindings WHERE project_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: CreateProjectRoleBinding :one
INSERT INTO project_role_bindings (user_id, "group", role_id, project_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: DeleteProjectRoleBinding :exec
DELETE FROM project_role_bindings WHERE id = $1;

-- name: ListUserBindingsWithRoles :many
-- One round-trip alternative to the per-scope ListBindings + per-binding
-- GetRoleByID fan-out used by the RBAC middleware. The scope discriminator
-- ('global' | 'cluster' | 'project') tells the Go side which scope columns
-- are meaningful for each row; unused columns are returned as NULL.
SELECT
    'global'::text                       AS scope,
    gb.id                                AS binding_id,
    gb."group"                           AS "group",
    gb.role_id                           AS role_id,
    NULL::uuid                           AS cluster_id,
    NULL::uuid                           AS project_id,
    gr.name                              AS role_name,
    gr.rules                             AS role_rules
FROM global_role_bindings gb
JOIN global_roles gr ON gr.id = gb.role_id
WHERE gb.user_id = $1
UNION ALL
SELECT
    'cluster'::text                      AS scope,
    cb.id                                AS binding_id,
    cb."group"                           AS "group",
    cb.role_id                           AS role_id,
    cb.cluster_id                        AS cluster_id,
    NULL::uuid                           AS project_id,
    cr.name                              AS role_name,
    cr.rules                             AS role_rules
FROM cluster_role_bindings cb
JOIN cluster_roles cr ON cr.id = cb.role_id
WHERE cb.user_id = $1
UNION ALL
SELECT
    'project'::text                      AS scope,
    pb.id                                AS binding_id,
    pb."group"                           AS "group",
    pb.role_id                           AS role_id,
    NULL::uuid                           AS cluster_id,
    pb.project_id                        AS project_id,
    pr.name                              AS role_name,
    pr.rules                             AS role_rules
FROM project_role_bindings pb
JOIN project_roles pr ON pr.id = pb.role_id
WHERE pb.user_id = $1;
