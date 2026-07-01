-- Native per-CRD RBAC rules (migration 126). Reference as q.<Name>.

-- name: CreateNativeRBACRule :one
INSERT INTO native_rbac_rules (
    user_id, cluster_id, namespace, api_group, resource, verbs, created_by_id
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, user_id, cluster_id, namespace, api_group, resource, verbs,
          created_at, created_by_id;

-- name: GetNativeRBACRuleByID :one
SELECT id, user_id, cluster_id, namespace, api_group, resource, verbs,
       created_at, created_by_id
FROM native_rbac_rules
WHERE id = $1;

-- name: ListNativeRBACRulesByUser :many
-- Both the CRUD/authoring view AND the authz-hook evaluation load use this:
-- a user's full rule set, newest first. The hot-path caller caches the result.
SELECT id, user_id, cluster_id, namespace, api_group, resource, verbs,
       created_at, created_by_id
FROM native_rbac_rules
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: ListNativeRBACRules :many
-- Admin overview across all users, paged.
SELECT id, user_id, cluster_id, namespace, api_group, resource, verbs,
       created_at, created_by_id
FROM native_rbac_rules
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: DeleteNativeRBACRule :exec
DELETE FROM native_rbac_rules WHERE id = $1;
