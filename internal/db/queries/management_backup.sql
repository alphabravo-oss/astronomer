-- Management-plane (Astronomer-itself) backup destinations.
-- Read/written by /api/v1/admin/management-backup/destinations/.

-- name: ListManagementBackupDestinations :many
SELECT * FROM management_backup_destinations
ORDER BY created_at ASC;

-- name: GetManagementBackupDestination :one
SELECT * FROM management_backup_destinations WHERE id = $1;

-- name: GetManagementBackupDestinationForUpdate :one
SELECT * FROM management_backup_destinations WHERE id = $1 FOR UPDATE;

-- name: CreateManagementBackupDestination :one
INSERT INTO management_backup_destinations (
    name, bucket, prefix, region, endpoint_url, encrypted_credentials,
    schedule, enabled, keep_daily, keep_weekly, keep_monthly, created_by_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
RETURNING *;

-- name: UpdateManagementBackupDestination :one
UPDATE management_backup_destinations SET
    name = $2,
    bucket = $3,
    prefix = $4,
    region = $5,
    endpoint_url = $6,
    encrypted_credentials = $7,
    schedule = $8,
    enabled = $9,
    keep_daily = $10,
    keep_weekly = $11,
    keep_monthly = $12,
    desired_generation = desired_generation + 1,
    desired_state = 'active',
    reconcile_status = 'pending',
    last_error = '',
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkManagementBackupDestinationDeleted :one
UPDATE management_backup_destinations
SET desired_generation = desired_generation + 1,
    desired_state = 'deleted', reconcile_status = 'pending', last_error = '', updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ClaimManagementBackupDestinationGeneration :one
UPDATE management_backup_destinations
SET reconcile_status = 'applying', last_error = '', updated_at = now()
WHERE id = sqlc.arg(id) AND desired_generation = sqlc.arg(generation)
  AND (reconcile_status IN ('pending', 'retrying')
       OR (reconcile_status = 'applying' AND updated_at < now() - interval '6 minutes'))
RETURNING *;

-- name: ClaimManagementBackupOperation :one
UPDATE workload_operations
SET status = 'running', attempt_count = attempt_count + 1, started_at = now(),
    completed_at = NULL, error_message = '', updated_at = now()
WHERE id = $1
  AND operation_type IN ('management_backup_test', 'management_backup_run')
  AND (status IN ('pending', 'retrying') OR (status = 'running' AND started_at < now() - interval '6 minutes'))
RETURNING *;

-- name: CompleteManagementBackupDestinationGeneration :one
UPDATE management_backup_destinations
SET applied_generation = sqlc.arg(generation), reconcile_status = 'ready', last_error = '',
    last_reconciled_at = now(), updated_at = now()
WHERE id = sqlc.arg(id) AND desired_generation = sqlc.arg(generation)
RETURNING *;

-- name: FailManagementBackupDestinationGeneration :one
UPDATE management_backup_destinations
SET reconcile_status = 'failed', last_error = left(sqlc.arg(error_message), 1024),
    last_reconciled_at = now(), updated_at = now()
WHERE id = sqlc.arg(id) AND desired_generation = sqlc.arg(generation)
RETURNING *;

-- name: RetryManagementBackupDestinationGeneration :one
UPDATE management_backup_destinations
SET reconcile_status = 'retrying', last_error = left(sqlc.arg(error_message), 1024),
    last_reconciled_at = now(), updated_at = now()
WHERE id = sqlc.arg(id) AND desired_generation = sqlc.arg(generation)
RETURNING *;
