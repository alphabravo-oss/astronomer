-- Quota plans CRUD --------------------------------------------------------

-- name: ListQuotaPlans :many
SELECT * FROM quota_plans ORDER BY name ASC;

-- name: GetQuotaPlan :one
SELECT * FROM quota_plans WHERE name = $1;

-- name: UpsertQuotaPlan :one
INSERT INTO quota_plans (
    name, enforcement, description,
    max_clusters_per_project, max_namespaces_per_project, max_members_per_project,
    max_projects_per_user, max_tokens_per_user, max_streams_per_user,
    max_total_clusters, max_total_users
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
ON CONFLICT (name) DO UPDATE SET
    enforcement                = EXCLUDED.enforcement,
    description                = EXCLUDED.description,
    max_clusters_per_project   = EXCLUDED.max_clusters_per_project,
    max_namespaces_per_project = EXCLUDED.max_namespaces_per_project,
    max_members_per_project    = EXCLUDED.max_members_per_project,
    max_projects_per_user      = EXCLUDED.max_projects_per_user,
    max_tokens_per_user        = EXCLUDED.max_tokens_per_user,
    max_streams_per_user       = EXCLUDED.max_streams_per_user,
    max_total_clusters         = EXCLUDED.max_total_clusters,
    max_total_users            = EXCLUDED.max_total_users,
    updated_at                 = now()
RETURNING *;

-- name: DeleteQuotaPlan :exec
DELETE FROM quota_plans WHERE name = $1;

-- name: CountProjectsUsingQuotaPlan :one
SELECT count(*) FROM projects WHERE quota_plan = $1;

-- name: CountUsersUsingQuotaPlan :one
SELECT count(*) FROM users WHERE quota_plan = $1;

-- Effective quota lookups ------------------------------------------------

-- name: GetEffectiveQuotaForUser :one
SELECT
    u.id              AS user_id,
    u.quota_plan      AS plan_name,
    u.quota_overrides AS overrides,
    p.enforcement,
    p.max_clusters_per_project,
    p.max_namespaces_per_project,
    p.max_members_per_project,
    p.max_projects_per_user,
    p.max_tokens_per_user,
    p.max_streams_per_user,
    p.max_total_clusters,
    p.max_total_users
FROM users u
JOIN quota_plans p ON p.name = u.quota_plan
WHERE u.id = $1;

-- name: GetEffectiveQuotaForProject :one
SELECT
    pr.id              AS project_id,
    pr.quota_plan      AS plan_name,
    pr.quota_overrides AS overrides,
    p.enforcement,
    p.max_clusters_per_project,
    p.max_namespaces_per_project,
    p.max_members_per_project,
    p.max_projects_per_user,
    p.max_tokens_per_user,
    p.max_streams_per_user,
    p.max_total_clusters,
    p.max_total_users
FROM projects pr
JOIN quota_plans p ON p.name = pr.quota_plan
WHERE pr.id = $1;

-- Per-tenant usage counters ----------------------------------------------

-- name: CountClustersInProject :one
SELECT count(*) FROM projects p2
WHERE p2.cluster_id = (SELECT p3.cluster_id FROM projects p3 WHERE p3.id = $1);

-- name: CountNamespacesInProject :one
SELECT COALESCE(jsonb_array_length(namespaces), 0)::int AS count
FROM projects WHERE id = $1;

-- name: CountMembersInProject :one
SELECT count(DISTINCT user_id)::bigint AS count
FROM project_role_bindings
WHERE project_id = $1 AND user_id IS NOT NULL;

-- name: CountProjectsForUser :one
SELECT count(DISTINCT project_id)::bigint AS count
FROM project_role_bindings
WHERE user_id = sqlc.arg(user_id)::uuid;

-- name: CountActiveTokensForUser :one
SELECT count(*)::bigint AS count
FROM api_tokens
WHERE user_id = $1 AND is_revoked = false;

-- Global / fleet-wide --------------------------------------------------

-- name: CountTotalClusters :one
SELECT count(*)::bigint AS count FROM clusters;

-- name: CountTotalActiveUsers :one
SELECT count(*)::bigint AS count FROM users WHERE is_active = true;

-- Usage snapshots for the admin dashboard --------------------------------

-- name: ListProjectQuotaSnapshots :many
SELECT
    pr.id   AS project_id,
    pr.name AS project_name,
    pr.quota_plan,
    p.enforcement,
    p.max_clusters_per_project,
    p.max_namespaces_per_project,
    p.max_members_per_project,
    (SELECT count(*) FROM projects pp WHERE pp.cluster_id = pr.cluster_id)::bigint                                            AS clusters_in_project,
    COALESCE(jsonb_array_length(pr.namespaces), 0)::bigint                                                                    AS namespaces_in_project,
    (SELECT count(DISTINCT user_id) FROM project_role_bindings WHERE project_id = pr.id AND user_id IS NOT NULL)::bigint      AS members_in_project
FROM projects pr
JOIN quota_plans p ON p.name = pr.quota_plan
ORDER BY pr.created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListUserQuotaSnapshots :many
SELECT
    u.id       AS user_id,
    u.username AS username,
    u.quota_plan,
    p.enforcement,
    p.max_projects_per_user,
    p.max_tokens_per_user,
    p.max_streams_per_user,
    (SELECT count(DISTINCT project_id) FROM project_role_bindings WHERE user_id = u.id)::bigint AS projects_for_user,
    (SELECT count(*) FROM api_tokens WHERE user_id = u.id AND is_revoked = false)::bigint        AS tokens_for_user
FROM users u
JOIN quota_plans p ON p.name = u.quota_plan
WHERE u.is_active = true
ORDER BY u.created_at DESC
LIMIT $1 OFFSET $2;
