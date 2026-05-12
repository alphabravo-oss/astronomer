-- Network policy templates + applications (migration 068).
-- Backs:
--   * /api/v1/admin/network-policy-templates/*  — superuser CRUD on templates
--   * /api/v1/clusters/{cluster_id}/network-policies/applications/*
--                                                — per-cluster apply/list/delete
--   * network_policy:apply worker  — reconcile each pending row via SSA
--   * network_policy:drift_check worker — periodic divergence sweep
--
-- The handler validates body shapes (slug regex, kind enum); this layer
-- stores/returns the values verbatim.

-- name: ListNetworkPolicyTemplates :many
SELECT * FROM network_policy_templates
ORDER BY kind DESC, name
LIMIT $1 OFFSET $2;

-- name: CountNetworkPolicyTemplates :one
SELECT count(*) FROM network_policy_templates;

-- name: GetNetworkPolicyTemplateByID :one
SELECT * FROM network_policy_templates WHERE id = $1;

-- name: GetNetworkPolicyTemplateBySlug :one
SELECT * FROM network_policy_templates WHERE slug = $1;

-- name: CreateNetworkPolicyTemplate :one
INSERT INTO network_policy_templates (slug, name, description, kind, spec_template, enabled, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateNetworkPolicyTemplate :one
-- Builtin rows are not edited via this path — the handler refuses with a
-- 403 before calling Update. The query still works on any row so an
-- operator-script with direct DB access can repair a broken builtin.
UPDATE network_policy_templates SET
    name           = $2,
    description    = $3,
    spec_template  = $4,
    enabled        = $5,
    updated_at     = now()
WHERE id = $1
RETURNING *;

-- name: DeleteNetworkPolicyTemplate :exec
DELETE FROM network_policy_templates WHERE id = $1;

-- name: ListNetworkPolicyApplications :many
SELECT * FROM network_policy_applications
ORDER BY updated_at DESC
LIMIT $1 OFFSET $2;

-- name: ListApplicationsForCluster :many
SELECT * FROM network_policy_applications
WHERE cluster_id = $1
ORDER BY updated_at DESC;

-- name: ListApplicationsForTemplate :many
SELECT * FROM network_policy_applications
WHERE template_id = $1
ORDER BY updated_at DESC;

-- name: ListPendingNetworkPolicyApplications :many
-- The reconciler picks up rows in {pending, failed, drifting}. 'failed'
-- rows are retried on every tick — combined with the apply-time
-- idempotence (SSA), this self-heals transient tunnel hiccups without
-- operator intervention. Cap defends against a "every app failed" stampede.
SELECT * FROM network_policy_applications
WHERE status IN ('pending', 'failed', 'drifting')
ORDER BY updated_at ASC
LIMIT $1;

-- name: ListAppliedNetworkPolicyApplications :many
-- For the drift sweep: walks only 'applied' rows, GETs the in-cluster
-- NetworkPolicy, compares to the rendered spec. Mismatch -> 'drifting'.
SELECT * FROM network_policy_applications
WHERE status = 'applied'
ORDER BY updated_at ASC
LIMIT $1;

-- name: GetNetworkPolicyApplicationByID :one
SELECT * FROM network_policy_applications WHERE id = $1;

-- name: GetNetworkPolicyApplicationByUnique :one
SELECT * FROM network_policy_applications
WHERE cluster_id = $1 AND namespace = $2 AND template_id = $3;

-- name: CreateNetworkPolicyApplication :one
INSERT INTO network_policy_applications
    (template_id, cluster_id, namespace, policy_name, status, applied_by)
VALUES ($1, $2, $3, $4, 'pending', $5)
RETURNING *;

-- name: DeleteNetworkPolicyApplication :exec
DELETE FROM network_policy_applications WHERE id = $1;

-- name: MarkNetworkPolicyApplicationStatus :one
-- Atomic transition used by the reconciler + drift check. When
-- last_applied_at is set the column is updated; passing the zero value
-- (Valid=false) leaves the existing timestamp untouched.
UPDATE network_policy_applications SET
    status          = $2,
    last_error      = $3,
    last_applied_at = CASE WHEN sqlc.arg(touch_applied)::bool THEN now() ELSE last_applied_at END,
    updated_at      = now()
WHERE id = $1
RETURNING *;

-- name: CountNetworkPolicyApplicationsByStatus :many
-- Powers the astronomer_network_policy_applications{cluster,status} gauge.
SELECT cluster_id, status, count(*)::bigint AS total
FROM network_policy_applications
GROUP BY cluster_id, status;
