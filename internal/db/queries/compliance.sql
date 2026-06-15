-- Compliance report export queries.
--
-- These power the SOC 2 / ISO 27001 audit-prep bundle the
-- `/api/v1/admin/compliance/export/` endpoint emits. The generated sqlc
-- output is the canonical Go API for these reads; keep this file parseable
-- by the pinned sqlc version used in CI.

-- name: ListAuditLogV1ForRange :many
-- Keyset-paginated stream across the partitioned audit_log table for
-- the export streamer. The compliance writer calls this in a loop
-- until the returned slice is shorter than the limit; ASC ordering on
-- (created_at, id) keeps the data consistent under concurrent
-- inserts (the existing ListAuditLogV1 sorts DESC, fine for the UI
-- but wrong for an export of millions of rows).
SELECT id, created_at, schema_version, source, correlation_id, user_id,
       actor_auth_method, action, resource_type, resource_id,
       resource_name, http_method, path, status_code, duration_ms,
       request_id, ip_address, user_agent, detail
FROM audit_log
WHERE created_at >= sqlc.arg(from_time)
  AND created_at <  sqlc.arg(to_time)
  AND (created_at, id) > (sqlc.arg(after_created_at), sqlc.arg(after_id)::uuid)
ORDER BY created_at ASC, id ASC
LIMIT sqlc.arg(page_limit);

-- name: CountAuditLogV1ForRange :one
-- The size estimate retained for compliance dashboards and future durable
-- background export planning. The current handler streams inline and keeps
-- async compliance exports disabled until durable job/output state exists.
SELECT count(*) FROM audit_log
WHERE created_at >= sqlc.arg(from_time)
  AND created_at <  sqlc.arg(to_time);

-- name: ListAllRoleBindingsWithRoleNames :many
-- All-users variant of ListUserBindingsWithRoles. Used by the
-- rbac-snapshot.csv writer. UNION across the three binding tables
-- joined with role names; the scope discriminator
-- ('global' | 'cluster' | 'project') tells the writer which scope
-- columns are meaningful. The COALESCE(source, 'manual') normalises
-- the column for pre-migration-042 rows.
SELECT
    'global'::text                  AS scope,
    gb.id                           AS binding_id,
    gb.user_id                      AS user_id,
    gb."group"                      AS "group",
    gb.role_id                      AS role_id,
    gr.name                         AS role_name,
    NULL::uuid                      AS cluster_id,
    NULL::uuid                      AS project_id,
    COALESCE(gb.source, 'manual')   AS source,
    gb.created_at                   AS created_at
FROM global_role_bindings gb
JOIN global_roles gr ON gr.id = gb.role_id
UNION ALL
SELECT
    'cluster'::text                 AS scope,
    cb.id                           AS binding_id,
    cb.user_id                      AS user_id,
    cb."group"                      AS "group",
    cb.role_id                      AS role_id,
    cr.name                         AS role_name,
    cb.cluster_id                   AS cluster_id,
    NULL::uuid                      AS project_id,
    COALESCE(cb.source, 'manual')   AS source,
    cb.created_at                   AS created_at
FROM cluster_role_bindings cb
JOIN cluster_roles cr ON cr.id = cb.role_id
UNION ALL
SELECT
    'project'::text                 AS scope,
    pb.id                           AS binding_id,
    pb.user_id                      AS user_id,
    pb."group"                      AS "group",
    pb.role_id                      AS role_id,
    pr.name                         AS role_name,
    NULL::uuid                      AS cluster_id,
    pb.project_id                   AS project_id,
    COALESCE(pb.source, 'manual')   AS source,
    pb.created_at                   AS created_at
FROM project_role_bindings pb
JOIN project_roles pr ON pr.id = pb.role_id
ORDER BY scope ASC, created_at ASC;

-- name: ListAllProjectsForCompliance :many
-- Every project with just the policy fields the compliance bundle
-- needs (pod_security_profile, network_policy_mode, resource_quota_*).
-- Distinct from ListProjects to keep the read cheap when there are
-- thousands of projects.
SELECT id, name, display_name, cluster_id,
       pod_security_profile, network_policy_mode,
       resource_quota_cpu_limit, resource_quota_memory_limit,
       resource_quota_pod_count, created_at
FROM projects
ORDER BY created_at ASC;

-- name: ListAPITokensForCompliance :many
-- Every API token row with user identity LEFT-JOINed in and the hash
-- + raw secret material stripped. `is_revoked = false` is NOT
-- filtered — auditors need the historical record of issued
-- credentials, including revoked ones. Selects `allowed_cidrs` +
-- `last_seen_remote_ip` from migration 044.
SELECT t.id, t.user_id, COALESCE(u.username, '') AS username,
       COALESCE(u.email, '') AS email,
       t.name, t.prefix, t.scopes,
       t.allowed_cidrs, t.last_seen_remote_ip,
       t.expires_at, t.last_used_at, t.is_revoked, t.created_at
FROM api_tokens t
LEFT JOIN users u ON u.id = t.user_id
ORDER BY t.created_at ASC;
