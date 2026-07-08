-- Phase: cluster decommission reconciler.
--
-- The handler enqueues a row via CreateClusterDecommission; the worker
-- claims it via MarkClusterDecommissionRunning (which bumps `attempts` and
-- stamps `started_at` ONCE via COALESCE — preserved across re-claims so the
-- graceExhausted wall-clock backstop measures from first claim, not last),
-- records per-phase progress via UpdateClusterDecommissionPhases,
-- and finally MarkClusterDecommissionSucceeded / MarkClusterDecommissionFailed
-- when all phases are done. The `phases` JSONB blob is rewritten in full each
-- time the reconciler advances — it's small and JSONB merge primitives in
-- pgx are a footgun; one-shot replace is the simpler contract.

-- name: CreateClusterDecommission :one
INSERT INTO cluster_decommissions (cluster_id, status, requested_by_id, cluster_name, force)
VALUES ($1, 'pending', $2, $3, $4)
RETURNING *;

-- name: SetClusterDecommissionForce :one
-- Escalate an already in-flight decommission to force so the reconciler stops
-- waiting out the cleanup grace window and tombstones on its next pass.
UPDATE cluster_decommissions SET force = true, updated_at = now()
WHERE id = $1
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
-- Lease-CAS claim. Claims the row only when it is pending/failed OR when a
-- prior runner's lease has expired (status='running' but updated_at older than
-- the lease TTL `$2` seconds). The active runner renews its lease implicitly on
-- every UpdateClusterDecommissionPhases (which bumps updated_at), so a healthy
-- in-flight runner is never preempted mid-RPC. When no row matches (a sibling
-- holds a live lease), the query returns no rows and the caller backs off — this
-- is the serialization point that stops the 1-minute periodic sweep from
-- double-running a row concurrently with the enqueued task.
UPDATE cluster_decommissions
SET
    status = 'running',
    attempts = attempts + 1,
    started_at = COALESCE(started_at, now()),
    last_error = '',
    updated_at = now()
WHERE id = $1
  AND (
      status IN ('pending', 'failed')
      OR (status = 'running' AND updated_at < now() - make_interval(secs => sqlc.arg(lease_ttl_seconds)::double precision))
  )
RETURNING *;

-- name: ReleaseClusterDecommissionClaim :exec
-- Releases the lease so a sibling pod can re-claim. Used by the HA re-queue
-- path: when the agent's WS is live on a SIBLING pod, the owning pod must be
-- able to claim the row, so the current (wrong) pod sets status back to
-- 'pending' before returning the task to asynq.
UPDATE cluster_decommissions
SET status = 'pending', updated_at = now()
WHERE id = $1;

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
DELETE FROM alert_rules WHERE cluster_id = sqlc.arg(cluster_id)::uuid;

-- name: DeleteAlertSilencesByCluster :execrows
DELETE FROM alert_silences WHERE cluster_id = sqlc.arg(cluster_id)::uuid;

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

-- name: DeleteClusterSnapshotSchedulesByCluster :execrows
-- Snapshot schedules are the actively-harmful orphan: the dispatcher
-- (ListEnabledSnapshotSchedules) keeps firing Velero backup jobs for a dead
-- cluster until these rows are gone. Tombstone semantics mean CASCADE never
-- fires, so remove them explicitly.
DELETE FROM cluster_snapshot_schedules WHERE cluster_id = $1;

-- name: DeleteGitOpsRegisteredClustersByCluster :execrows
DELETE FROM gitops_registered_clusters WHERE cluster_id = $1;

-- name: DeleteNativeRBACRulesByCluster :execrows
DELETE FROM native_rbac_rules WHERE cluster_id = sqlc.arg(cluster_id)::uuid;

-- name: DeleteDeferredOperationsByCluster :execrows
DELETE FROM deferred_operations WHERE target_cluster_id = sqlc.arg(cluster_id)::uuid;

-- name: DeleteAgentLifecycleOperationsByCluster :execrows
DELETE FROM agent_lifecycle_operations WHERE cluster_id = $1;

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

-- name: ArchiveAndPurgeAuditLogsForCluster :execrows
-- Atomic archive-then-delete used by the decommission archive_audit phase.
--
-- A single statement so both halves see ONE snapshot: to_archive pins the exact
-- set of matching audit_log rows, the INSERT copies that set into audit_archive
-- (ON CONFLICT DO NOTHING keeps re-runs idempotent), and the DELETE removes
-- EXACTLY that same set. A row committed by a concurrent request after the
-- statement snapshot is not in to_archive, so it is neither archived nor deleted
-- here — it survives in audit_log and is picked up on the next decommission
-- re-run, instead of being DELETEd-but-never-archived (silent audit loss). The
-- single uuid arg is cast to text for the resource_id / detail->>'cluster_id'
-- comparisons (uuid::text is the canonical lowercase form Go's uuid.String()
-- also produces).
WITH to_archive AS (
    SELECT
        id, created_at, schema_version, user_id, actor_auth_method,
        action, resource_type, resource_id, resource_name,
        http_method, path, status_code, duration_ms, request_id,
        ip_address, user_agent, detail, source, correlation_id
    FROM audit_log
    WHERE
        (resource_type = 'cluster' AND resource_id = sqlc.arg(cluster_id)::uuid::text)
        OR (detail ->> 'cluster_id') = sqlc.arg(cluster_id)::uuid::text
), inserted AS (
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
    FROM to_archive
    ON CONFLICT (id, created_at) DO NOTHING
)
DELETE FROM audit_log al
USING to_archive ta
WHERE al.id = ta.id AND al.created_at = ta.created_at;
