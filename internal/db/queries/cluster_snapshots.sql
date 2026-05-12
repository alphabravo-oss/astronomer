-- Per-cluster Velero snapshot + restore self-service (migration 052).
--
-- Every query in this file targets one of:
--   - cluster_snapshots
--   - cluster_restores
--   - cluster_snapshot_schedules
--
-- Note for maintainers: the actual Go bodies live in
-- internal/db/sqlc/cluster_snapshots_ext.sql.go (hand-authored shim) —
-- the SQL text below is the canonical source, kept in this directory so
-- sqlc generate against a workstation continues to round-trip cleanly.

-- ====== cluster_snapshots ================================================

-- name: ListClusterSnapshots :many
SELECT id, cluster_id, velero_name, velero_namespace, source, spec, phase,
       start_time, completion_time, expires_at,
       warnings_count, errors_count, last_poll_at, last_poll_error,
       created_by, created_at, updated_at
FROM cluster_snapshots
WHERE cluster_id = $1
ORDER BY created_at DESC;

-- name: GetClusterSnapshotByID :one
SELECT id, cluster_id, velero_name, velero_namespace, source, spec, phase,
       start_time, completion_time, expires_at,
       warnings_count, errors_count, last_poll_at, last_poll_error,
       created_by, created_at, updated_at
FROM cluster_snapshots
WHERE id = $1;

-- name: CreateClusterSnapshot :one
INSERT INTO cluster_snapshots (
    cluster_id, velero_name, velero_namespace, source, spec, phase, expires_at, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, cluster_id, velero_name, velero_namespace, source, spec, phase,
          start_time, completion_time, expires_at,
          warnings_count, errors_count, last_poll_at, last_poll_error,
          created_by, created_at, updated_at;

-- name: DeleteClusterSnapshot :exec
DELETE FROM cluster_snapshots WHERE id = $1;

-- name: ListPendingClusterSnapshots :many
SELECT id, cluster_id, velero_name, velero_namespace, source, spec, phase,
       start_time, completion_time, expires_at,
       warnings_count, errors_count, last_poll_at, last_poll_error,
       created_by, created_at, updated_at
FROM cluster_snapshots
WHERE phase IN ('New', 'InProgress')
ORDER BY created_at ASC
LIMIT $1;

-- name: ListExpiredTerminalSnapshots :many
SELECT id, cluster_id, velero_name, velero_namespace, source, spec, phase,
       start_time, completion_time, expires_at,
       warnings_count, errors_count, last_poll_at, last_poll_error,
       created_by, created_at, updated_at
FROM cluster_snapshots
WHERE expires_at IS NOT NULL
  AND expires_at < now()
  AND phase IN ('Completed','Failed','FailedValidation','PartiallyFailed','Deleted')
ORDER BY expires_at ASC
LIMIT $1;

-- name: MarkSnapshotPhase :exec
UPDATE cluster_snapshots
SET phase           = $2,
    start_time      = $3,
    completion_time = $4,
    warnings_count  = $5,
    errors_count    = $6,
    last_poll_at    = now(),
    last_poll_error = $7,
    updated_at      = now()
WHERE id = $1;

-- ====== cluster_restores =================================================

-- name: ListClusterRestores :many
SELECT id, snapshot_id, target_cluster_id, velero_name, velero_namespace,
       spec, phase, start_time, completion_time,
       warnings_count, errors_count, last_poll_at, last_poll_error,
       created_by, created_at, updated_at
FROM cluster_restores
WHERE target_cluster_id = $1
ORDER BY created_at DESC;

-- name: GetClusterRestoreByID :one
SELECT id, snapshot_id, target_cluster_id, velero_name, velero_namespace,
       spec, phase, start_time, completion_time,
       warnings_count, errors_count, last_poll_at, last_poll_error,
       created_by, created_at, updated_at
FROM cluster_restores
WHERE id = $1;

-- name: CreateClusterRestore :one
INSERT INTO cluster_restores (
    snapshot_id, target_cluster_id, velero_name, velero_namespace, spec, phase, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, snapshot_id, target_cluster_id, velero_name, velero_namespace,
          spec, phase, start_time, completion_time,
          warnings_count, errors_count, last_poll_at, last_poll_error,
          created_by, created_at, updated_at;

-- name: ListPendingClusterRestores :many
SELECT id, snapshot_id, target_cluster_id, velero_name, velero_namespace,
       spec, phase, start_time, completion_time,
       warnings_count, errors_count, last_poll_at, last_poll_error,
       created_by, created_at, updated_at
FROM cluster_restores
WHERE phase IN ('New','InProgress')
ORDER BY created_at ASC
LIMIT $1;

-- name: MarkRestorePhase :exec
UPDATE cluster_restores
SET phase           = $2,
    start_time      = $3,
    completion_time = $4,
    warnings_count  = $5,
    errors_count    = $6,
    last_poll_at    = now(),
    last_poll_error = $7,
    updated_at      = now()
WHERE id = $1;

-- ====== cluster_snapshot_schedules =======================================

-- name: ListClusterSnapshotSchedules :many
SELECT id, cluster_id, name, cron_schedule, spec, enabled,
       last_run_at, last_run_status, created_by, created_at, updated_at
FROM cluster_snapshot_schedules
WHERE cluster_id = $1
ORDER BY name ASC;

-- name: GetClusterSnapshotScheduleByID :one
SELECT id, cluster_id, name, cron_schedule, spec, enabled,
       last_run_at, last_run_status, created_by, created_at, updated_at
FROM cluster_snapshot_schedules
WHERE id = $1;

-- name: ListEnabledSnapshotSchedules :many
SELECT id, cluster_id, name, cron_schedule, spec, enabled,
       last_run_at, last_run_status, created_by, created_at, updated_at
FROM cluster_snapshot_schedules
WHERE enabled = true
ORDER BY id ASC;

-- name: CreateClusterSnapshotSchedule :one
INSERT INTO cluster_snapshot_schedules (
    cluster_id, name, cron_schedule, spec, enabled, created_by
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, cluster_id, name, cron_schedule, spec, enabled,
          last_run_at, last_run_status, created_by, created_at, updated_at;

-- name: UpdateClusterSnapshotSchedule :one
UPDATE cluster_snapshot_schedules
SET name          = $2,
    cron_schedule = $3,
    spec          = $4,
    enabled       = $5,
    updated_at    = now()
WHERE id = $1
RETURNING id, cluster_id, name, cron_schedule, spec, enabled,
          last_run_at, last_run_status, created_by, created_at, updated_at;

-- name: DeleteClusterSnapshotSchedule :exec
DELETE FROM cluster_snapshot_schedules WHERE id = $1;

-- name: MarkSnapshotScheduleRan :exec
UPDATE cluster_snapshot_schedules
SET last_run_at     = now(),
    last_run_status = $2,
    updated_at      = now()
WHERE id = $1;
