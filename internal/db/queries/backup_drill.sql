-- Backup restore drill results — written by the
-- management-plane-restore-drill-cronjob, read by the
-- /api/v1/admin/backup-drill/ admin endpoint.

-- name: GetLatestBackupDrillResult :one
-- The summary endpoint shows ONLY the most recent drill — that's enough
-- for the "are we current?" question. History uses ListBackupDrillResults.
SELECT * FROM backup_drill_results
ORDER BY started_at DESC
LIMIT 1;

-- name: GetLatestSuccessfulBackupDrillResult :one
-- Surfaces "when did we last *prove* the backups work?". Distinct from
-- the latest row because the most recent drill may have failed; the
-- staleness alert fires on the gap from the latest *success*, not the
-- latest attempt.
SELECT * FROM backup_drill_results
WHERE status = 'success'
ORDER BY started_at DESC
LIMIT 1;

-- name: ListBackupDrillResults :many
-- Paginated history for the admin UI.
SELECT * FROM backup_drill_results
ORDER BY started_at DESC
LIMIT $1 OFFSET $2;

-- name: CountBackupDrillResults :one
SELECT count(*) FROM backup_drill_results;
