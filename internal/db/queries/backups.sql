-- Backup Storage Configs

-- name: GetBackupStorageConfigByID :one
SELECT * FROM backup_storage_configs WHERE id = $1;

-- name: ListBackupStorageConfigs :many
SELECT * FROM backup_storage_configs ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: GetDefaultBackupStorageConfig :one
SELECT * FROM backup_storage_configs WHERE is_default = true LIMIT 1;

-- name: CreateBackupStorageConfig :one
INSERT INTO backup_storage_configs (
    name, storage_type, bucket, prefix, region, endpoint_url,
    access_key, secret_key, is_default, created_by_id,
    cluster_id, velero_namespace, bsl_name, encrypted_credentials
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING *;

-- name: UpdateBackupStorageConfig :one
UPDATE backup_storage_configs SET
    name = $2,
    storage_type = $3,
    bucket = $4,
    prefix = $5,
    region = $6,
    endpoint_url = $7,
    access_key = $8,
    secret_key = $9,
    is_default = $10,
    cluster_id = $11,
    velero_namespace = $12,
    bsl_name = $13,
    encrypted_credentials = $14
WHERE id = $1
RETURNING *;

-- name: DeleteBackupStorageConfig :exec
DELETE FROM backup_storage_configs WHERE id = $1;

-- name: CountBackupStorageConfigs :one
SELECT count(*) FROM backup_storage_configs;

-- Backups

-- name: GetBackupByID :one
SELECT * FROM backups WHERE id = $1;

-- name: ListBackups :many
SELECT * FROM backups ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListBackupsByStorage :many
SELECT * FROM backups WHERE storage_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListBackupsByStatus :many
SELECT * FROM backups WHERE status = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListRunningBackupsForPolling :many
SELECT * FROM backups
WHERE status = 'running' AND velero_backup_name <> ''
ORDER BY last_polled_at NULLS FIRST, created_at ASC
LIMIT $1;

-- name: CreateBackup :one
INSERT INTO backups (
    name, storage_id, backup_type, status, database_tables, created_by_id,
    cluster_id, velero_backup_name, velero_namespace, included_namespaces, excluded_namespaces
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: UpdateBackupStatus :exec
UPDATE backups SET
    status = $2,
    file_path = $3,
    file_size_bytes = $4,
    error_message = $5
WHERE id = $1;

-- name: UpdateBackupStarted :exec
UPDATE backups SET status = 'running', started_at = now() WHERE id = $1;

-- name: UpdateBackupCompleted :exec
UPDATE backups SET status = 'completed', completed_at = now(), file_path = $2, file_size_bytes = $3 WHERE id = $1;

-- name: UpdateBackupFailed :exec
UPDATE backups SET status = 'failed', completed_at = now(), error_message = $2 WHERE id = $1;

-- name: UpdateBackupVeleroIdentity :exec
UPDATE backups SET
    velero_backup_name = $2,
    velero_namespace   = $3,
    cluster_id         = $4
WHERE id = $1;

-- name: TouchBackupPolling :exec
UPDATE backups SET
    last_polled_at = now(),
    poll_attempts  = poll_attempts + 1
WHERE id = $1;

-- name: DeleteBackup :exec
DELETE FROM backups WHERE id = $1;

-- name: CountBackups :one
SELECT count(*) FROM backups;

-- Backup Schedules

-- name: GetBackupScheduleByID :one
SELECT * FROM backup_schedules WHERE id = $1;

-- name: ListBackupSchedules :many
SELECT * FROM backup_schedules ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: GetActiveSchedules :many
SELECT * FROM backup_schedules WHERE enabled = true ORDER BY created_at ASC;

-- name: CreateBackupSchedule :one
INSERT INTO backup_schedules (
    name, storage_id, backup_type, cron_expression, retention_count, enabled, created_by_id,
    cluster_id, velero_namespace, velero_schedule_name, included_namespaces, excluded_namespaces, ttl
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: UpdateBackupSchedule :one
UPDATE backup_schedules SET
    name = $2,
    storage_id = $3,
    backup_type = $4,
    cron_expression = $5,
    retention_count = $6,
    enabled = $7,
    cluster_id = $8,
    velero_namespace = $9,
    velero_schedule_name = $10,
    included_namespaces = $11,
    excluded_namespaces = $12,
    ttl = $13
WHERE id = $1
RETURNING *;

-- name: UpdateBackupScheduleLastBackup :exec
UPDATE backup_schedules SET last_backup_id = $2 WHERE id = $1;

-- name: DeleteBackupSchedule :exec
DELETE FROM backup_schedules WHERE id = $1;

-- name: CountBackupSchedules :one
SELECT count(*) FROM backup_schedules;

-- Restore Operations

-- name: GetRestoreOperationByID :one
SELECT * FROM restore_operations WHERE id = $1;

-- name: ListRestoreOperations :many
SELECT * FROM restore_operations ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListRunningRestoresForPolling :many
SELECT * FROM restore_operations
WHERE status = 'running' AND velero_restore_name <> ''
ORDER BY last_polled_at NULLS FIRST, created_at ASC
LIMIT $1;

-- name: ListRestoreOperationsByBackup :many
SELECT * FROM restore_operations WHERE backup_id = $1 ORDER BY created_at DESC;

-- name: CreateRestoreOperation :one
INSERT INTO restore_operations (
    backup_id, status, initiated_by_id,
    cluster_id, velero_namespace, velero_restore_name, included_namespaces, namespace_mapping
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateRestoreOperationStarted :exec
UPDATE restore_operations SET status = 'running', started_at = now() WHERE id = $1;

-- name: UpdateRestoreOperationCompleted :exec
UPDATE restore_operations SET status = 'completed', completed_at = now() WHERE id = $1;

-- name: UpdateRestoreOperationFailed :exec
UPDATE restore_operations SET status = 'failed', completed_at = now(), error_message = $2 WHERE id = $1;

-- name: UpdateRestoreVeleroIdentity :exec
UPDATE restore_operations SET
    velero_restore_name = $2,
    velero_namespace    = $3,
    cluster_id          = $4
WHERE id = $1;

-- name: TouchRestorePolling :exec
UPDATE restore_operations SET
    last_polled_at = now(),
    poll_attempts  = poll_attempts + 1
WHERE id = $1;

-- name: DeleteRestoreOperation :exec
DELETE FROM restore_operations WHERE id = $1;

-- name: CountRestoreOperations :one
SELECT count(*) FROM restore_operations;
