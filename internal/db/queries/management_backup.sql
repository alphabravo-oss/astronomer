-- Management-plane (Astronomer-itself) backup destinations.
-- Read/written by /api/v1/admin/management-backup/destinations/.

-- name: ListManagementBackupDestinations :many
SELECT * FROM management_backup_destinations
ORDER BY created_at ASC;

-- name: GetManagementBackupDestination :one
SELECT * FROM management_backup_destinations WHERE id = $1;

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
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteManagementBackupDestination :exec
DELETE FROM management_backup_destinations WHERE id = $1;
