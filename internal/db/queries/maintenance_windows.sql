-- Maintenance windows + deferred operations (migration 057).
--
-- The window evaluator reads ListEnabledMaintenanceWindows() into an
-- in-memory cache (30s TTL) so the per-mutation check is cheap. The
-- deferred-ops worker scans ListPendingDeferredOperations every 60s and
-- re-fires the rows whose deferred_until has elapsed.

-- name: ListMaintenanceWindows :many
SELECT id, name, description, mode, cron_open, duration_minutes, timezone,
       cluster_selector, operation_types, on_block, enabled,
       created_by, created_at, updated_at
FROM maintenance_windows
ORDER BY name ASC;

-- name: ListEnabledMaintenanceWindows :many
SELECT id, name, description, mode, cron_open, duration_minutes, timezone,
       cluster_selector, operation_types, on_block, enabled,
       created_by, created_at, updated_at
FROM maintenance_windows
WHERE enabled = true
ORDER BY name ASC;

-- name: GetMaintenanceWindow :one
SELECT id, name, description, mode, cron_open, duration_minutes, timezone,
       cluster_selector, operation_types, on_block, enabled,
       created_by, created_at, updated_at
FROM maintenance_windows
WHERE id = $1;

-- name: GetMaintenanceWindowByName :one
SELECT id, name, description, mode, cron_open, duration_minutes, timezone,
       cluster_selector, operation_types, on_block, enabled,
       created_by, created_at, updated_at
FROM maintenance_windows
WHERE name = $1;

-- name: CreateMaintenanceWindow :one
INSERT INTO maintenance_windows (
    name, description, mode, cron_open, duration_minutes, timezone,
    cluster_selector, operation_types, on_block, enabled, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, name, description, mode, cron_open, duration_minutes, timezone,
          cluster_selector, operation_types, on_block, enabled,
          created_by, created_at, updated_at;

-- name: UpdateMaintenanceWindow :one
UPDATE maintenance_windows
SET name              = $2,
    description       = $3,
    mode              = $4,
    cron_open         = $5,
    duration_minutes  = $6,
    timezone          = $7,
    cluster_selector  = $8,
    operation_types   = $9,
    on_block          = $10,
    enabled           = $11,
    updated_at        = now()
WHERE id = $1
RETURNING id, name, description, mode, cron_open, duration_minutes, timezone,
          cluster_selector, operation_types, on_block, enabled,
          created_by, created_at, updated_at;

-- name: DeleteMaintenanceWindow :exec
DELETE FROM maintenance_windows WHERE id = $1;

-- Deferred operations --------------------------------------------------

-- name: CreateDeferredOperation :one
INSERT INTO deferred_operations (
    window_id, operation_type, operation_spec, target_cluster_id, target_project_id,
    deferred_until, expires_at, requested_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, window_id, operation_type, operation_spec, target_cluster_id,
          target_project_id, status, deferred_until, expires_at, requested_by,
          last_error, dispatched_at, created_at, updated_at;

-- name: GetDeferredOperation :one
SELECT id, window_id, operation_type, operation_spec, target_cluster_id,
       target_project_id, status, deferred_until, expires_at, requested_by,
       last_error, dispatched_at, created_at, updated_at
FROM deferred_operations
WHERE id = $1;

-- name: ListDeferredOperations :many
SELECT id, window_id, operation_type, operation_spec, target_cluster_id,
       target_project_id, status, deferred_until, expires_at, requested_by,
       last_error, dispatched_at, created_at, updated_at
FROM deferred_operations
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListPendingDeferredOperations :many
-- The dispatcher pulls rows whose deferred_until has elapsed. The
-- partial index idx_deferred_operations_pending makes this scan cheap.
SELECT id, window_id, operation_type, operation_spec, target_cluster_id,
       target_project_id, status, deferred_until, expires_at, requested_by,
       last_error, dispatched_at, created_at, updated_at
FROM deferred_operations
WHERE status = 'pending'
  AND (deferred_until IS NULL OR deferred_until <= $1)
ORDER BY deferred_until ASC NULLS FIRST
LIMIT $2;

-- name: MarkDeferredDispatched :exec
UPDATE deferred_operations
SET status        = 'dispatched',
    dispatched_at = $2,
    updated_at    = now()
WHERE id = $1;

-- name: MarkDeferredExpired :exec
UPDATE deferred_operations
SET status     = 'expired',
    last_error = $2,
    updated_at = now()
WHERE id = $1;

-- name: MarkDeferredCancelled :exec
UPDATE deferred_operations
SET status     = 'cancelled',
    last_error = $2,
    updated_at = now()
WHERE id = $1;

-- name: MarkDeferredFailed :exec
UPDATE deferred_operations
SET last_error = $2,
    updated_at = now()
WHERE id = $1;

-- name: CountDeferredOperations :one
SELECT COUNT(*) FROM deferred_operations;
