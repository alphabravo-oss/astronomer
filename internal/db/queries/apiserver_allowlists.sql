-- Apiserver allow-list CRUD (migration 070).
--
-- Hand-edited SQL for the apiserver_allowlists + apiserver_allowlist_
-- snapshots tables. The sqlc generator produces a thin Go shim with
-- type-safe arguments around these queries.

-- name: GetApiserverAllowlistByClusterID :one
SELECT cluster_id, cidrs, mode, detected_provider, last_reconciled_at,
       sync_status, last_error, effective_cidrs, created_at, updated_at
FROM apiserver_allowlists
WHERE cluster_id = $1;

-- name: ListApiserverAllowlists :many
SELECT cluster_id, cidrs, mode, detected_provider, last_reconciled_at,
       sync_status, last_error, effective_cidrs, created_at, updated_at
FROM apiserver_allowlists
ORDER BY cluster_id;

-- name: ListActiveApiserverAllowlists :many
-- "active" == mode != 'disabled' — the rows the reconciler walks every tick.
SELECT cluster_id, cidrs, mode, detected_provider, last_reconciled_at,
       sync_status, last_error, effective_cidrs, created_at, updated_at
FROM apiserver_allowlists
WHERE mode != 'disabled'
ORDER BY cluster_id;

-- name: UpsertApiserverAllowlist :one
INSERT INTO apiserver_allowlists (
    cluster_id, cidrs, mode
) VALUES ($1, $2, $3)
ON CONFLICT (cluster_id) DO UPDATE SET
    cidrs      = EXCLUDED.cidrs,
    mode       = EXCLUDED.mode,
    updated_at = now()
RETURNING cluster_id, cidrs, mode, detected_provider, last_reconciled_at,
          sync_status, last_error, effective_cidrs, created_at, updated_at;

-- name: UpdateApiserverAllowlistReconcileState :exec
-- Stamps the per-tick outcome — provider, sync_status, last_error,
-- effective_cidrs snapshot, last_reconciled_at. Called by the reconciler.
UPDATE apiserver_allowlists
SET detected_provider  = $2,
    sync_status        = $3,
    last_error         = $4,
    effective_cidrs    = $5,
    last_reconciled_at = now(),
    updated_at         = now()
WHERE cluster_id = $1;

-- name: DeleteApiserverAllowlist :exec
DELETE FROM apiserver_allowlists WHERE cluster_id = $1;

-- Snapshots ------------------------------------------------------------

-- name: InsertApiserverAllowlistSnapshot :one
INSERT INTO apiserver_allowlist_snapshots (
    cluster_id, effective_cidrs, desired_cidrs, drift
) VALUES ($1, $2, $3, $4)
RETURNING id, cluster_id, captured_at, effective_cidrs, desired_cidrs, drift;

-- name: ListApiserverAllowlistSnapshots :many
SELECT id, cluster_id, captured_at, effective_cidrs, desired_cidrs, drift
FROM apiserver_allowlist_snapshots
WHERE cluster_id = $1
ORDER BY captured_at DESC
LIMIT $2 OFFSET $3;

-- name: DeleteApiserverAllowlistSnapshotsOlderThan :exec
-- 90-day retention sweep — drop rows older than the given cutoff.
DELETE FROM apiserver_allowlist_snapshots
WHERE captured_at < $1;
