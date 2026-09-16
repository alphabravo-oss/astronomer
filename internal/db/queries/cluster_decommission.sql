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

-- name: ClaimPendingClusterDecommissions :many
-- Atomically claims a fair, bounded batch for a periodic sweep. Live leases
-- are excluded before LIMIT, so an old in-flight prefix cannot starve later
-- rows. updated_at is advanced on every attempt/completion and is the primary
-- ordering key, moving repeatedly failing work behind rows not yet attempted.
WITH candidates AS (
    SELECT id
    FROM cluster_decommissions
    WHERE status IN ('pending', 'failed')
       OR (
           status = 'running'
           AND (
               decommission_lease_until IS NULL
               OR decommission_lease_until <= now()
           )
       )
    ORDER BY updated_at ASC, created_at ASC, id ASC
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(query_limit)
)
UPDATE cluster_decommissions AS decommission
SET status = 'running',
    attempts = attempts + 1,
    started_at = COALESCE(started_at, now()),
    last_error = '',
    updated_at = now(),
    decommission_claim_token = sqlc.arg(claim_token),
    decommission_lease_until = now() + make_interval(secs => sqlc.arg(lease_ttl_seconds)::double precision)
FROM candidates
WHERE decommission.id = candidates.id
RETURNING decommission.*;

-- name: ListPendingClusterDecommissionsForClusters :many
-- Page enrichment is scoped to the rows already authorized and returned. Keep
-- tenant filtering in SQL and avoid scanning the estate-wide in-flight set.
SELECT * FROM cluster_decommissions
WHERE status IN ('pending', 'running')
  AND cluster_id = ANY(sqlc.arg(cluster_ids)::uuid[])
ORDER BY created_at ASC;

-- name: MarkClusterDecommissionRunning :one
-- Claims one enqueue-time task. A fresh opaque token fences every subsequent
-- state write; an expired owner can never overwrite its replacement.
UPDATE cluster_decommissions
SET
    status = 'running',
    attempts = attempts + 1,
    started_at = COALESCE(started_at, now()),
    last_error = '',
    updated_at = now(),
    decommission_claim_token = sqlc.arg(claim_token),
    decommission_lease_until = now() + make_interval(secs => sqlc.arg(lease_ttl_seconds)::double precision)
WHERE id = sqlc.arg(id)
  AND (
      status IN ('pending', 'failed')
      OR (
          status = 'running'
          AND (
              decommission_lease_until IS NULL
              OR decommission_lease_until <= now()
          )
      )
  )
RETURNING *;

-- name: RenewClusterDecommissionClaim :execrows
-- Long-running side effects renew ownership in the background. A zero row
-- count means the lease was superseded and the runner must stop.
UPDATE cluster_decommissions
SET decommission_lease_until = now() + make_interval(secs => sqlc.arg(lease_ttl_seconds)::double precision)
WHERE id = sqlc.arg(id)
  AND status = 'running'
  AND decommission_claim_token = sqlc.arg(claim_token);

-- name: ReleaseClusterDecommissionClaim :execrows
-- Releases the lease so a sibling pod can re-claim. Used by the HA re-queue
-- path: when the agent's WS is live on a SIBLING pod, the owning pod must be
-- able to claim the row, so the current (wrong) pod sets status back to
-- 'pending' before returning the task to asynq. Token fencing prevents an
-- expired owner from releasing a replacement owner's claim.
UPDATE cluster_decommissions
SET status = 'pending',
    updated_at = now(),
    decommission_claim_token = NULL,
    decommission_lease_until = NULL
WHERE id = sqlc.arg(id)
  AND status = 'running'
  AND decommission_claim_token = sqlc.arg(claim_token);

-- name: UpdateClusterDecommissionPhases :one
UPDATE cluster_decommissions
SET
    phases = sqlc.arg(phases),
    updated_at = now(),
    decommission_lease_until = now() + make_interval(secs => sqlc.arg(lease_ttl_seconds)::double precision)
WHERE id = sqlc.arg(id)
  AND status = 'running'
  AND decommission_claim_token = sqlc.arg(claim_token)
RETURNING *;

-- name: MarkClusterDecommissionSucceeded :one
UPDATE cluster_decommissions
SET
    status = 'succeeded',
    completed_at = now(),
    last_error = '',
    updated_at = now(),
    phases = sqlc.arg(phases),
    decommission_claim_token = NULL,
    decommission_lease_until = NULL
WHERE id = sqlc.arg(id)
  AND status = 'running'
  AND decommission_claim_token = sqlc.arg(claim_token)
RETURNING *;

-- name: MarkClusterDecommissionFailed :one
UPDATE cluster_decommissions
SET
    status = 'failed',
    completed_at = now(),
    last_error = sqlc.arg(last_error),
    phases = sqlc.arg(phases),
    updated_at = now(),
    decommission_claim_token = NULL,
    decommission_lease_until = NULL
WHERE id = sqlc.arg(id)
  AND status = 'running'
  AND decommission_claim_token = sqlc.arg(claim_token)
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
-- The archive_audit phase uses only the atomic archive-and-purge statement
-- below. The former split INSERT/DELETE queries were removed because exposing
-- either half made it possible to delete a different snapshot than the one
-- copied into the archive.

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
        archived_cluster_id, archived_cluster_name
    )
    SELECT
        id, created_at, schema_version, user_id, actor_auth_method,
        action, resource_type, resource_id, resource_name,
        http_method, path, status_code, duration_ms, request_id,
        ip_address, user_agent, detail, source, correlation_id,
        sqlc.arg(cluster_id)::uuid,
        COALESCE((SELECT COALESCE(NULLIF(c.display_name, ''), c.name) FROM clusters c WHERE c.id = sqlc.arg(cluster_id)::uuid), '')
    FROM to_archive
    ON CONFLICT (id, created_at) DO NOTHING
)
DELETE FROM audit_log al
USING to_archive ta
WHERE al.id = ta.id AND al.created_at = ta.created_at;
