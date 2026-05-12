// Migration 060 — GitOps cluster registration: sources + tracked-clusters
// CRUD, hand-authored sqlc shim.
//
// The repo's sqlc CLI is occasionally not runnable in agent worktrees; we
// follow the same hand-rolled pattern as cluster_registry_configs_ext.sql.go
// and cloud_credentials.sql.go so the build keeps passing across
// successive sqlc-generate runs.

package sqlc

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// GitopsRegistrationSource is one tracked Git repo.
type GitopsRegistrationSource struct {
	ID                  uuid.UUID          `json:"id"`
	Name                string             `json:"name"`
	RepoUrl             string             `json:"repo_url"`
	Branch              string             `json:"branch"`
	PathPrefix          string             `json:"path_prefix"`
	AuthMode            string             `json:"auth_mode"`
	AuthEncrypted       string             `json:"auth_encrypted"`
	SyncMode            string             `json:"sync_mode"`
	SyncIntervalSeconds int32              `json:"sync_interval_seconds"`
	OnDelete            string             `json:"on_delete"`
	LastSyncedAt        pgtype.Timestamptz `json:"last_synced_at"`
	LastSyncedSha       string             `json:"last_synced_sha"`
	LastError           string             `json:"last_error"`
	Enabled             bool               `json:"enabled"`
	CreatedBy           pgtype.UUID        `json:"created_by"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
}

// GitopsRegisteredCluster is the link between a clusters row and the
// source that owns it.
type GitopsRegisteredCluster struct {
	ClusterID     uuid.UUID          `json:"cluster_id"`
	SourceID      uuid.UUID          `json:"source_id"`
	RepoPath      string             `json:"repo_path"`
	LastYamlSha   string             `json:"last_yaml_sha"`
	LastAppliedAt time.Time          `json:"last_applied_at"`
	Status        string             `json:"status"`
	TombstonedAt  pgtype.Timestamptz `json:"tombstoned_at"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

const gitopsSourceColumns = `id, name, repo_url, branch, path_prefix, auth_mode, auth_encrypted, sync_mode, sync_interval_seconds, on_delete, last_synced_at, last_synced_sha, last_error, enabled, created_by, created_at, updated_at`

func scanGitopsRegistrationSourceRow(row interface {
	Scan(dest ...any) error
}) (GitopsRegistrationSource, error) {
	var i GitopsRegistrationSource
	err := row.Scan(
		&i.ID,
		&i.Name,
		&i.RepoUrl,
		&i.Branch,
		&i.PathPrefix,
		&i.AuthMode,
		&i.AuthEncrypted,
		&i.SyncMode,
		&i.SyncIntervalSeconds,
		&i.OnDelete,
		&i.LastSyncedAt,
		&i.LastSyncedSha,
		&i.LastError,
		&i.Enabled,
		&i.CreatedBy,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listGitOpsSources = `-- name: ListGitOpsSources :many
SELECT ` + gitopsSourceColumns + `
FROM gitops_registration_sources
ORDER BY name ASC`

func (q *Queries) ListGitOpsSources(ctx context.Context) ([]GitopsRegistrationSource, error) {
	rows, err := q.db.Query(ctx, listGitOpsSources)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []GitopsRegistrationSource{}
	for rows.Next() {
		i, err := scanGitopsRegistrationSourceRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listEnabledGitOpsSources = `-- name: ListEnabledGitOpsSources :many
SELECT ` + gitopsSourceColumns + `
FROM gitops_registration_sources
WHERE enabled = true
ORDER BY name ASC`

func (q *Queries) ListEnabledGitOpsSources(ctx context.Context) ([]GitopsRegistrationSource, error) {
	rows, err := q.db.Query(ctx, listEnabledGitOpsSources)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []GitopsRegistrationSource{}
	for rows.Next() {
		i, err := scanGitopsRegistrationSourceRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getGitOpsSource = `-- name: GetGitOpsSource :one
SELECT ` + gitopsSourceColumns + `
FROM gitops_registration_sources
WHERE id = $1`

func (q *Queries) GetGitOpsSource(ctx context.Context, id uuid.UUID) (GitopsRegistrationSource, error) {
	row := q.db.QueryRow(ctx, getGitOpsSource, id)
	return scanGitopsRegistrationSourceRow(row)
}

const getGitOpsSourceByName = `-- name: GetGitOpsSourceByName :one
SELECT ` + gitopsSourceColumns + `
FROM gitops_registration_sources
WHERE name = $1`

func (q *Queries) GetGitOpsSourceByName(ctx context.Context, name string) (GitopsRegistrationSource, error) {
	row := q.db.QueryRow(ctx, getGitOpsSourceByName, name)
	return scanGitopsRegistrationSourceRow(row)
}

const createGitOpsSource = `-- name: CreateGitOpsSource :one
INSERT INTO gitops_registration_sources (
    name, repo_url, branch, path_prefix, auth_mode, auth_encrypted,
    sync_mode, sync_interval_seconds, on_delete, enabled, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING ` + gitopsSourceColumns

type CreateGitOpsSourceParams struct {
	Name                string      `json:"name"`
	RepoUrl             string      `json:"repo_url"`
	Branch              string      `json:"branch"`
	PathPrefix          string      `json:"path_prefix"`
	AuthMode            string      `json:"auth_mode"`
	AuthEncrypted       string      `json:"auth_encrypted"`
	SyncMode            string      `json:"sync_mode"`
	SyncIntervalSeconds int32       `json:"sync_interval_seconds"`
	OnDelete            string      `json:"on_delete"`
	Enabled             bool        `json:"enabled"`
	CreatedBy           pgtype.UUID `json:"created_by"`
}

func (q *Queries) CreateGitOpsSource(ctx context.Context, arg CreateGitOpsSourceParams) (GitopsRegistrationSource, error) {
	row := q.db.QueryRow(ctx, createGitOpsSource,
		arg.Name,
		arg.RepoUrl,
		arg.Branch,
		arg.PathPrefix,
		arg.AuthMode,
		arg.AuthEncrypted,
		arg.SyncMode,
		arg.SyncIntervalSeconds,
		arg.OnDelete,
		arg.Enabled,
		arg.CreatedBy,
	)
	return scanGitopsRegistrationSourceRow(row)
}

const updateGitOpsSource = `-- name: UpdateGitOpsSource :one
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
RETURNING ` + gitopsSourceColumns

type UpdateGitOpsSourceParams struct {
	ID                  uuid.UUID `json:"id"`
	Name                string    `json:"name"`
	RepoUrl             string    `json:"repo_url"`
	Branch              string    `json:"branch"`
	PathPrefix          string    `json:"path_prefix"`
	AuthMode            string    `json:"auth_mode"`
	AuthEncrypted       string    `json:"auth_encrypted"`
	SyncMode            string    `json:"sync_mode"`
	SyncIntervalSeconds int32     `json:"sync_interval_seconds"`
	OnDelete            string    `json:"on_delete"`
	Enabled             bool      `json:"enabled"`
}

func (q *Queries) UpdateGitOpsSource(ctx context.Context, arg UpdateGitOpsSourceParams) (GitopsRegistrationSource, error) {
	row := q.db.QueryRow(ctx, updateGitOpsSource,
		arg.ID,
		arg.Name,
		arg.RepoUrl,
		arg.Branch,
		arg.PathPrefix,
		arg.AuthMode,
		arg.AuthEncrypted,
		arg.SyncMode,
		arg.SyncIntervalSeconds,
		arg.OnDelete,
		arg.Enabled,
	)
	return scanGitopsRegistrationSourceRow(row)
}

const deleteGitOpsSource = `-- name: DeleteGitOpsSource :exec
DELETE FROM gitops_registration_sources WHERE id = $1`

func (q *Queries) DeleteGitOpsSource(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteGitOpsSource, id)
	return err
}

const stampGitOpsSourceSync = `-- name: StampGitOpsSourceSync :exec
UPDATE gitops_registration_sources
SET last_synced_at  = $2,
    last_synced_sha = $3,
    last_error      = '',
    updated_at      = now()
WHERE id = $1`

type StampGitOpsSourceSyncParams struct {
	ID            uuid.UUID          `json:"id"`
	LastSyncedAt  pgtype.Timestamptz `json:"last_synced_at"`
	LastSyncedSha string             `json:"last_synced_sha"`
}

func (q *Queries) StampGitOpsSourceSync(ctx context.Context, arg StampGitOpsSourceSyncParams) error {
	_, err := q.db.Exec(ctx, stampGitOpsSourceSync, arg.ID, arg.LastSyncedAt, arg.LastSyncedSha)
	return err
}

const stampGitOpsSourceError = `-- name: StampGitOpsSourceError :exec
UPDATE gitops_registration_sources
SET last_error = $2,
    updated_at = now()
WHERE id = $1`

type StampGitOpsSourceErrorParams struct {
	ID        uuid.UUID `json:"id"`
	LastError string    `json:"last_error"`
}

func (q *Queries) StampGitOpsSourceError(ctx context.Context, arg StampGitOpsSourceErrorParams) error {
	_, err := q.db.Exec(ctx, stampGitOpsSourceError, arg.ID, arg.LastError)
	return err
}

// Registered clusters --------------------------------------------------

const gitopsRegisteredClusterColumns = `cluster_id, source_id, repo_path, last_yaml_sha, last_applied_at, status, tombstoned_at, created_at, updated_at`

func scanGitopsRegisteredClusterRow(row interface {
	Scan(dest ...any) error
}) (GitopsRegisteredCluster, error) {
	var i GitopsRegisteredCluster
	err := row.Scan(
		&i.ClusterID,
		&i.SourceID,
		&i.RepoPath,
		&i.LastYamlSha,
		&i.LastAppliedAt,
		&i.Status,
		&i.TombstonedAt,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listGitOpsRegisteredClustersBySource = `-- name: ListGitOpsRegisteredClustersBySource :many
SELECT ` + gitopsRegisteredClusterColumns + `
FROM gitops_registered_clusters
WHERE source_id = $1
ORDER BY repo_path ASC`

func (q *Queries) ListGitOpsRegisteredClustersBySource(ctx context.Context, sourceID uuid.UUID) ([]GitopsRegisteredCluster, error) {
	rows, err := q.db.Query(ctx, listGitOpsRegisteredClustersBySource, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []GitopsRegisteredCluster{}
	for rows.Next() {
		i, err := scanGitopsRegisteredClusterRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getGitOpsRegisteredCluster = `-- name: GetGitOpsRegisteredCluster :one
SELECT ` + gitopsRegisteredClusterColumns + `
FROM gitops_registered_clusters
WHERE cluster_id = $1`

func (q *Queries) GetGitOpsRegisteredCluster(ctx context.Context, clusterID uuid.UUID) (GitopsRegisteredCluster, error) {
	row := q.db.QueryRow(ctx, getGitOpsRegisteredCluster, clusterID)
	return scanGitopsRegisteredClusterRow(row)
}

const upsertGitOpsRegisteredCluster = `-- name: UpsertGitOpsRegisteredCluster :one
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
RETURNING ` + gitopsRegisteredClusterColumns

type UpsertGitOpsRegisteredClusterParams struct {
	ClusterID   uuid.UUID `json:"cluster_id"`
	SourceID    uuid.UUID `json:"source_id"`
	RepoPath    string    `json:"repo_path"`
	LastYamlSha string    `json:"last_yaml_sha"`
}

func (q *Queries) UpsertGitOpsRegisteredCluster(ctx context.Context, arg UpsertGitOpsRegisteredClusterParams) (GitopsRegisteredCluster, error) {
	row := q.db.QueryRow(ctx, upsertGitOpsRegisteredCluster,
		arg.ClusterID,
		arg.SourceID,
		arg.RepoPath,
		arg.LastYamlSha,
	)
	return scanGitopsRegisteredClusterRow(row)
}

const tombstoneGitOpsRegisteredCluster = `-- name: TombstoneGitOpsRegisteredCluster :exec
UPDATE gitops_registered_clusters
SET status        = 'tombstoned',
    tombstoned_at = $2,
    updated_at    = now()
WHERE cluster_id = $1`

type TombstoneGitOpsRegisteredClusterParams struct {
	ClusterID    uuid.UUID          `json:"cluster_id"`
	TombstonedAt pgtype.Timestamptz `json:"tombstoned_at"`
}

func (q *Queries) TombstoneGitOpsRegisteredCluster(ctx context.Context, arg TombstoneGitOpsRegisteredClusterParams) error {
	_, err := q.db.Exec(ctx, tombstoneGitOpsRegisteredCluster, arg.ClusterID, arg.TombstonedAt)
	return err
}

const deleteGitOpsRegisteredCluster = `-- name: DeleteGitOpsRegisteredCluster :exec
DELETE FROM gitops_registered_clusters WHERE cluster_id = $1`

func (q *Queries) DeleteGitOpsRegisteredCluster(ctx context.Context, clusterID uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteGitOpsRegisteredCluster, clusterID)
	return err
}

const listExpiredTombstones = `-- name: ListExpiredTombstones :many
SELECT ` + gitopsRegisteredClusterColumns + `
FROM gitops_registered_clusters
WHERE status = 'tombstoned'
  AND tombstoned_at IS NOT NULL
  AND tombstoned_at <= $1
ORDER BY tombstoned_at ASC`

func (q *Queries) ListExpiredTombstones(ctx context.Context, cutoff pgtype.Timestamptz) ([]GitopsRegisteredCluster, error) {
	rows, err := q.db.Query(ctx, listExpiredTombstones, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []GitopsRegisteredCluster{}
	for rows.Next() {
		i, err := scanGitopsRegisteredClusterRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const countGitOpsRegisteredClustersBySource = `-- name: CountGitOpsRegisteredClustersBySource :one
SELECT COUNT(*) FROM gitops_registered_clusters WHERE source_id = $1`

func (q *Queries) CountGitOpsRegisteredClustersBySource(ctx context.Context, sourceID uuid.UUID) (int64, error) {
	row := q.db.QueryRow(ctx, countGitOpsRegisteredClustersBySource, sourceID)
	var n int64
	err := row.Scan(&n)
	return n, err
}

const countGitOpsTombstonedBySource = `-- name: CountGitOpsTombstonedBySource :one
SELECT COUNT(*) FROM gitops_registered_clusters
WHERE source_id = $1 AND status = 'tombstoned'`

func (q *Queries) CountGitOpsTombstonedBySource(ctx context.Context, sourceID uuid.UUID) (int64, error) {
	row := q.db.QueryRow(ctx, countGitOpsTombstonedBySource, sourceID)
	var n int64
	err := row.Scan(&n)
	return n, err
}
