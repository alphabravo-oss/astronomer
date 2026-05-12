-- GitOps cluster registration sources + tracked clusters (migration 060).
--
-- The sync worker pulls ListEnabledGitOpsSources every 60s, fetches each
-- repo, walks the YAML files, and reconciles via UpsertGitOpsRegistered
-- + StampGitOpsSourceSync. The reaper sweeps tombstoned rows older than
-- the 24h grace via ListExpiredTombstones, enqueueing
-- cluster:decommission for each. The handler tier owns CRUD over the
-- sources themselves and the per-source /clusters/ + /preview/ readers.

-- name: ListGitOpsSources :many
SELECT id, name, repo_url, branch, path_prefix, auth_mode, auth_encrypted,
       sync_mode, sync_interval_seconds, on_delete,
       last_synced_at, last_synced_sha, last_error, enabled,
       created_by, created_at, updated_at
FROM gitops_registration_sources
ORDER BY name ASC;

-- name: ListEnabledGitOpsSources :many
SELECT id, name, repo_url, branch, path_prefix, auth_mode, auth_encrypted,
       sync_mode, sync_interval_seconds, on_delete,
       last_synced_at, last_synced_sha, last_error, enabled,
       created_by, created_at, updated_at
FROM gitops_registration_sources
WHERE enabled = true
ORDER BY name ASC;

-- name: GetGitOpsSource :one
SELECT id, name, repo_url, branch, path_prefix, auth_mode, auth_encrypted,
       sync_mode, sync_interval_seconds, on_delete,
       last_synced_at, last_synced_sha, last_error, enabled,
       created_by, created_at, updated_at
FROM gitops_registration_sources
WHERE id = $1;

-- name: GetGitOpsSourceByName :one
SELECT id, name, repo_url, branch, path_prefix, auth_mode, auth_encrypted,
       sync_mode, sync_interval_seconds, on_delete,
       last_synced_at, last_synced_sha, last_error, enabled,
       created_by, created_at, updated_at
FROM gitops_registration_sources
WHERE name = $1;

-- name: CreateGitOpsSource :one
INSERT INTO gitops_registration_sources (
    name, repo_url, branch, path_prefix, auth_mode, auth_encrypted,
    sync_mode, sync_interval_seconds, on_delete, enabled, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, name, repo_url, branch, path_prefix, auth_mode, auth_encrypted,
          sync_mode, sync_interval_seconds, on_delete,
          last_synced_at, last_synced_sha, last_error, enabled,
          created_by, created_at, updated_at;

-- name: UpdateGitOpsSource :one
UPDATE gitops_registration_sources
SET name                  = $2,
    repo_url              = $3,
    branch                = $4,
    path_prefix           = $5,
    auth_mode             = $6,
    auth_encrypted        = $7,
    sync_mode             = $8,
    sync_interval_seconds = $9,
    on_delete             = $10,
    enabled               = $11,
    updated_at            = now()
WHERE id = $1
RETURNING id, name, repo_url, branch, path_prefix, auth_mode, auth_encrypted,
          sync_mode, sync_interval_seconds, on_delete,
          last_synced_at, last_synced_sha, last_error, enabled,
          created_by, created_at, updated_at;

-- name: DeleteGitOpsSource :exec
DELETE FROM gitops_registration_sources WHERE id = $1;

-- name: StampGitOpsSourceSync :exec
-- Called by the sync worker after every successful tick. Clearing
-- last_error on success is intentional — partial failures during a tick
-- leave last_error stamped via StampGitOpsSourceError.
UPDATE gitops_registration_sources
SET last_synced_at  = $2,
    last_synced_sha = $3,
    last_error      = '',
    updated_at      = now()
WHERE id = $1;

-- name: StampGitOpsSourceError :exec
-- Stamped on a hard sync failure (clone error, walk error, etc).
UPDATE gitops_registration_sources
SET last_error = $2,
    updated_at = now()
WHERE id = $1;

-- Registered clusters --------------------------------------------------

-- name: ListGitOpsRegisteredClustersBySource :many
SELECT cluster_id, source_id, repo_path, last_yaml_sha, last_applied_at,
       status, tombstoned_at, created_at, updated_at
FROM gitops_registered_clusters
WHERE source_id = $1
ORDER BY repo_path ASC;

-- name: GetGitOpsRegisteredCluster :one
SELECT cluster_id, source_id, repo_path, last_yaml_sha, last_applied_at,
       status, tombstoned_at, created_at, updated_at
FROM gitops_registered_clusters
WHERE cluster_id = $1;

-- name: UpsertGitOpsRegisteredCluster :one
-- The sync worker calls this after a YAML's contents have been applied
-- so subsequent ticks no-op when last_yaml_sha matches. ON CONFLICT
-- promotes any tombstoned row back to active — that's the
-- "YAML reappears" path under on_delete='tombstone'.
INSERT INTO gitops_registered_clusters (
    cluster_id, source_id, repo_path, last_yaml_sha, last_applied_at, status, tombstoned_at
) VALUES ($1, $2, $3, $4, now(), 'active', NULL)
ON CONFLICT (cluster_id) DO UPDATE
SET source_id       = EXCLUDED.source_id,
    repo_path       = EXCLUDED.repo_path,
    last_yaml_sha   = EXCLUDED.last_yaml_sha,
    last_applied_at = now(),
    status          = 'active',
    tombstoned_at   = NULL,
    updated_at      = now()
RETURNING cluster_id, source_id, repo_path, last_yaml_sha, last_applied_at,
          status, tombstoned_at, created_at, updated_at;

-- name: TombstoneGitOpsRegisteredCluster :exec
-- Sets status='tombstoned' + tombstoned_at=now. The reaper later picks
-- the row up via ListExpiredTombstones.
UPDATE gitops_registered_clusters
SET status        = 'tombstoned',
    tombstoned_at = $2,
    updated_at    = now()
WHERE cluster_id = $1;

-- name: DeleteGitOpsRegisteredCluster :exec
DELETE FROM gitops_registered_clusters WHERE cluster_id = $1;

-- name: ListExpiredTombstones :many
-- The reaper pulls rows that have been tombstoned for longer than the
-- grace window (24h by default — passed in as the parameter so tests
-- can shrink it). The partial index idx_gitops_tombstoned_clusters
-- keeps this scan cheap as the table grows.
SELECT cluster_id, source_id, repo_path, last_yaml_sha, last_applied_at,
       status, tombstoned_at, created_at, updated_at
FROM gitops_registered_clusters
WHERE status = 'tombstoned'
  AND tombstoned_at IS NOT NULL
  AND tombstoned_at <= $1
ORDER BY tombstoned_at ASC;

-- name: CountGitOpsRegisteredClustersBySource :one
SELECT COUNT(*) FROM gitops_registered_clusters WHERE source_id = $1;

-- name: CountGitOpsTombstonedBySource :one
SELECT COUNT(*) FROM gitops_registered_clusters
WHERE source_id = $1 AND status = 'tombstoned';
