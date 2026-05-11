-- Phase: cluster decommission reconciler.
--
-- The handler enqueues a row via CreateClusterDecommission; the worker
-- claims it via MarkClusterDecommissionRunning (which bumps `attempts` and
-- sets `started_at`), records per-phase progress via UpdateClusterDecommissionPhases,
-- and finally MarkClusterDecommissionSucceeded / MarkClusterDecommissionFailed
-- when all phases are done. The `phases` JSONB blob is rewritten in full each
-- time the reconciler advances — it's small and JSONB merge primitives in
-- pgx are a footgun; one-shot replace is the simpler contract.

-- name: CreateClusterDecommission :one
INSERT INTO cluster_decommissions (cluster_id, status, requested_by_id, cluster_name)
VALUES ($1, 'pending', $2, $3)
RETURNING *;

-- name: GetClusterDecommissionByID :one
SELECT * FROM cluster_decommissions WHERE id = $1;

-- name: GetLatestClusterDecommissionByCluster :one
SELECT * FROM cluster_decommissions
WHERE cluster_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: ListPendingClusterDecommissions :many
-- Used by the periodic sweep to find decommissions that need re-runs (the
-- enqueue-time task may have been lost or the reconciler may have crashed
-- mid-phase).
SELECT * FROM cluster_decommissions
WHERE status IN ('pending', 'running')
ORDER BY created_at ASC
LIMIT $1;

-- name: MarkClusterDecommissionRunning :one
UPDATE cluster_decommissions
SET
    status = 'running',
    attempts = attempts + 1,
    started_at = COALESCE(started_at, now()),
    last_error = '',
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateClusterDecommissionPhases :one
UPDATE cluster_decommissions
SET
    phases = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkClusterDecommissionSucceeded :one
UPDATE cluster_decommissions
SET
    status = 'succeeded',
    completed_at = now(),
    last_error = '',
    updated_at = now(),
    phases = $2
WHERE id = $1
RETURNING *;

-- name: MarkClusterDecommissionFailed :one
UPDATE cluster_decommissions
SET
    status = 'failed',
    completed_at = now(),
    last_error = $2,
    phases = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- Cluster tombstone — the final phase of the reconciler. We never hard-delete
-- the cluster row; setting decommissioned_at preserves the id for audit_archive
-- referential integrity and lets the UI render historical references.

-- name: TombstoneCluster :exec
UPDATE clusters
SET
    decommissioned_at = now(),
    status = 'decommissioned',
    updated_at = now()
WHERE id = $1;

-- Dependent row cleanup: every table that holds a cluster_id FK has its
-- entries removed here. The CASCADE behaviour on the original FK definitions
-- means most of these would be implicitly removed by hard-deleting the
-- cluster row — but since the reconciler tombstones rather than DELETEs,
-- we have to do the cleanup explicitly. Each query is :execrows so the
-- worker can include "rows removed per table" in its phase outcome.

-- name: DeleteClusterRegistrationTokensByCluster :execrows
DELETE FROM cluster_registration_tokens WHERE cluster_id = $1;

-- name: DeleteClusterAgentTokensByCluster :execrows
DELETE FROM cluster_agent_tokens WHERE cluster_id = $1;

-- name: DeleteClusterRegistryConfigsByCluster :execrows
DELETE FROM cluster_registry_configs WHERE cluster_id = $1;

-- name: DeleteClusterHealthStatusByCluster :execrows
DELETE FROM cluster_health_statuses WHERE cluster_id = $1;

-- name: DeleteClusterConditionsByCluster :execrows
DELETE FROM cluster_conditions WHERE cluster_id = $1;

-- name: DeleteAgentConnectionsByCluster :execrows
DELETE FROM agent_connections WHERE cluster_id = $1;

-- name: DeleteAlertRulesByCluster :execrows
DELETE FROM alert_rules WHERE cluster_id = $1;

-- name: DeleteAlertSilencesByCluster :execrows
DELETE FROM alert_silences WHERE cluster_id = $1;

-- name: DeleteInstalledChartsByCluster :execrows
DELETE FROM installed_charts WHERE cluster_id = $1;

-- name: DeleteClusterSecurityPoliciesByCluster :execrows
DELETE FROM cluster_security_policies WHERE cluster_id = $1;

-- (cluster_tools is a catalog table holding built-in tool definitions;
-- it has no cluster_id and is intentionally NOT touched by the
-- decommission reconciler. Per-cluster tool state lives in
-- installed_charts and tool_operations.)

-- name: DeleteProjectNamespacesByCluster :execrows
DELETE FROM project_namespaces WHERE cluster_id = $1;

-- name: DeleteClusterRoleBindingsByCluster :execrows
DELETE FROM cluster_role_bindings WHERE cluster_id = $1;

-- Audit archive operations.
--
-- ArchiveAuditLogsForCluster is the bulk INSERT … SELECT used during the
-- archive_audit phase. The cluster id is looked up in two places: resource_id
-- (when the row was emitted with resource_type='cluster') and the
-- detail->>'cluster_id' field (when an unrelated resource row tagged itself
-- with the cluster). The detail extraction uses ->> so it's a text comparison
-- against the cluster_id as a string.

-- name: ArchiveAuditLogsForCluster :execrows
INSERT INTO audit_archive (
    id, created_at, schema_version, user_id, actor_auth_method,
    action, resource_type, resource_id, resource_name,
    http_method, path, status_code, duration_ms, request_id,
    ip_address, user_agent, detail, source, correlation_id,
    archived_cluster_id
)
SELECT
    id, created_at, schema_version, user_id, actor_auth_method,
    action, resource_type, resource_id, resource_name,
    http_method, path, status_code, duration_ms, request_id,
    ip_address, user_agent, detail, source, correlation_id,
    sqlc.arg(cluster_id)::uuid
FROM audit_log
WHERE
    (resource_type = 'cluster' AND resource_id = sqlc.arg(cluster_id_text)::text)
    OR (detail ->> 'cluster_id') = sqlc.arg(cluster_id_text)::text
ON CONFLICT (id, created_at) DO NOTHING;

-- name: DeleteAuditLogsForCluster :execrows
-- Run AFTER ArchiveAuditLogsForCluster; removes the now-archived rows from
-- the live audit_log partition tree.
DELETE FROM audit_log
WHERE
    (resource_type = 'cluster' AND resource_id = sqlc.arg(cluster_id_text)::text)
    OR (detail ->> 'cluster_id') = sqlc.arg(cluster_id_text)::text;
