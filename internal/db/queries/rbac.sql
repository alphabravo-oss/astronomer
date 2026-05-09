-- Global Roles

-- name: GetGlobalRoleByID :one
SELECT * FROM global_roles WHERE id = $1;

-- name: GetGlobalRoleByName :one
SELECT * FROM global_roles WHERE name = $1;

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

-- name: GetUserGlobalRoles :many
SELECT gr.* FROM global_roles gr
INNER JOIN global_role_bindings grb ON gr.id = grb.role_id
WHERE grb.user_id = $1;

-- name: GetGlobalRoleBindingsByUserID :many
SELECT * FROM global_role_bindings WHERE user_id = $1;

-- name: GetGlobalRoleBindingsByGroup :many
SELECT * FROM global_role_bindings WHERE "group" = $1;

-- name: CreateGlobalRoleBinding :one
INSERT INTO global_role_bindings (user_id, "group", role_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: DeleteGlobalRoleBinding :exec
DELETE FROM global_role_bindings WHERE id = $1;

-- Cluster Roles

-- name: GetClusterRoleByID :one
SELECT * FROM cluster_roles WHERE id = $1;

-- name: GetClusterRoleByName :one
SELECT * FROM cluster_roles WHERE name = $1;

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

-- name: GetUserClusterRoles :many
SELECT cr.* FROM cluster_roles cr
INNER JOIN cluster_role_bindings crb ON cr.id = crb.role_id
WHERE crb.user_id = $1 AND crb.cluster_id = $2;

-- name: GetClusterRoleBindingsByUserID :many
SELECT * FROM cluster_role_bindings WHERE user_id = $1;

-- name: GetClusterRoleBindingsByGroup :many
SELECT * FROM cluster_role_bindings WHERE "group" = $1;

-- name: CreateClusterRoleBinding :one
INSERT INTO cluster_role_bindings (user_id, "group", role_id, cluster_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: DeleteClusterRoleBinding :exec
DELETE FROM cluster_role_bindings WHERE id = $1;

-- Project Roles

-- name: GetProjectRoleByID :one
SELECT * FROM project_roles WHERE id = $1;

-- name: GetProjectRoleByName :one
SELECT * FROM project_roles WHERE name = $1;

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

-- name: GetUserProjectRoles :many
SELECT pr.* FROM project_roles pr
INNER JOIN project_role_bindings prb ON pr.id = prb.role_id
WHERE prb.user_id = $1 AND prb.project_id = $2;

-- name: GetProjectRoleBindingsByUserID :many
SELECT * FROM project_role_bindings WHERE user_id = $1;

-- name: GetProjectRoleBindingsByGroup :many
SELECT * FROM project_role_bindings WHERE "group" = $1;

-- name: CreateProjectRoleBinding :one
INSERT INTO project_role_bindings (user_id, "group", role_id, project_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: DeleteProjectRoleBinding :exec
DELETE FROM project_role_bindings WHERE id = $1;
