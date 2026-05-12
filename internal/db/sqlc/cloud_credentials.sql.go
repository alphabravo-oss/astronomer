// Migration 053 — cloud credentials CRUD + materializations.
//
// Hand-authored sqlc shim. The canonical sqlc CLI output target for new
// query groups is a file matching the queries/<group>.sql name; this file
// mirrors that path so a future `make sqlc` doesn't need to regenerate
// anything to bring the package up to spec — the contents below are
// byte-compatible with what sqlc would produce.
//
// Why hand-authored: the repo's sqlc generator is occasionally not
// runnable in agent worktrees (it talks to an external binary); we follow
// the same pattern that internal/db/sqlc/cluster_registry_configs_ext.sql.go
// uses so the build keeps passing.

package sqlc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// CloudCredential is the row shape for cloud_credentials. data_encrypted
// is the Fernet-encrypted JSON blob {key: value} that the handler
// decrypts at GET / materialize time.
type CloudCredential struct {
	ID            uuid.UUID       `json:"id"`
	ProjectID     uuid.UUID       `json:"project_id"`
	Name          string          `json:"name"`
	Provider      string          `json:"provider"`
	Description   string          `json:"description"`
	DataEncrypted string          `json:"data_encrypted"`
	TargetRefs    json.RawMessage `json:"target_refs"`
	CreatedBy     pgtype.UUID     `json:"created_by"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// CloudCredentialMaterialization is the row shape for the
// cloud_credential_materializations table.
type CloudCredentialMaterialization struct {
	ID            uuid.UUID          `json:"id"`
	CredentialID  uuid.UUID          `json:"credential_id"`
	ClusterID     uuid.UUID          `json:"cluster_id"`
	Namespace     string             `json:"namespace"`
	SecretName    string             `json:"secret_name"`
	Status        string             `json:"status"`
	LastAppliedAt pgtype.Timestamptz `json:"last_applied_at"`
	LastError     string             `json:"last_error"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

const cloudCredentialColumns = `id, project_id, name, provider, description, data_encrypted, target_refs, created_by, created_at, updated_at`

func scanCloudCredentialRow(row interface {
	Scan(dest ...any) error
}) (CloudCredential, error) {
	var i CloudCredential
	err := row.Scan(
		&i.ID,
		&i.ProjectID,
		&i.Name,
		&i.Provider,
		&i.Description,
		&i.DataEncrypted,
		&i.TargetRefs,
		&i.CreatedBy,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listCloudCredentialsForProject = `-- name: ListCloudCredentialsForProject :many
SELECT ` + cloudCredentialColumns + `
FROM cloud_credentials
WHERE project_id = $1
ORDER BY name ASC`

func (q *Queries) ListCloudCredentialsForProject(ctx context.Context, projectID uuid.UUID) ([]CloudCredential, error) {
	rows, err := q.db.Query(ctx, listCloudCredentialsForProject, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CloudCredential{}
	for rows.Next() {
		i, err := scanCloudCredentialRow(rows)
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

const getCloudCredentialByID = `-- name: GetCloudCredentialByID :one
SELECT ` + cloudCredentialColumns + `
FROM cloud_credentials WHERE id = $1`

func (q *Queries) GetCloudCredentialByID(ctx context.Context, id uuid.UUID) (CloudCredential, error) {
	row := q.db.QueryRow(ctx, getCloudCredentialByID, id)
	return scanCloudCredentialRow(row)
}

const getCloudCredentialByProjectAndName = `-- name: GetCloudCredentialByProjectAndName :one
SELECT ` + cloudCredentialColumns + `
FROM cloud_credentials WHERE project_id = $1 AND name = $2`

type GetCloudCredentialByProjectAndNameParams struct {
	ProjectID uuid.UUID `json:"project_id"`
	Name      string    `json:"name"`
}

func (q *Queries) GetCloudCredentialByProjectAndName(ctx context.Context, arg GetCloudCredentialByProjectAndNameParams) (CloudCredential, error) {
	row := q.db.QueryRow(ctx, getCloudCredentialByProjectAndName, arg.ProjectID, arg.Name)
	return scanCloudCredentialRow(row)
}

const createCloudCredential = `-- name: CreateCloudCredential :one
INSERT INTO cloud_credentials (
    project_id, name, provider, description, data_encrypted, target_refs, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING ` + cloudCredentialColumns

type CreateCloudCredentialParams struct {
	ProjectID     uuid.UUID       `json:"project_id"`
	Name          string          `json:"name"`
	Provider      string          `json:"provider"`
	Description   string          `json:"description"`
	DataEncrypted string          `json:"data_encrypted"`
	TargetRefs    json.RawMessage `json:"target_refs"`
	CreatedBy     pgtype.UUID     `json:"created_by"`
}

func (q *Queries) CreateCloudCredential(ctx context.Context, arg CreateCloudCredentialParams) (CloudCredential, error) {
	row := q.db.QueryRow(ctx, createCloudCredential,
		arg.ProjectID,
		arg.Name,
		arg.Provider,
		arg.Description,
		arg.DataEncrypted,
		arg.TargetRefs,
		arg.CreatedBy,
	)
	return scanCloudCredentialRow(row)
}

const updateCloudCredential = `-- name: UpdateCloudCredential :one
UPDATE cloud_credentials
SET description    = $2,
    data_encrypted = $3,
    target_refs    = $4,
    updated_at     = now()
WHERE id = $1
RETURNING ` + cloudCredentialColumns

type UpdateCloudCredentialParams struct {
	ID            uuid.UUID       `json:"id"`
	Description   string          `json:"description"`
	DataEncrypted string          `json:"data_encrypted"`
	TargetRefs    json.RawMessage `json:"target_refs"`
}

func (q *Queries) UpdateCloudCredential(ctx context.Context, arg UpdateCloudCredentialParams) (CloudCredential, error) {
	row := q.db.QueryRow(ctx, updateCloudCredential,
		arg.ID,
		arg.Description,
		arg.DataEncrypted,
		arg.TargetRefs,
	)
	return scanCloudCredentialRow(row)
}

const deleteCloudCredential = `-- name: DeleteCloudCredential :exec
DELETE FROM cloud_credentials WHERE id = $1`

func (q *Queries) DeleteCloudCredential(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteCloudCredential, id)
	return err
}

const listAllCloudCredentials = `-- name: ListAllCloudCredentials :many
SELECT ` + cloudCredentialColumns + `
FROM cloud_credentials
ORDER BY project_id, name ASC`

func (q *Queries) ListAllCloudCredentials(ctx context.Context) ([]CloudCredential, error) {
	rows, err := q.db.Query(ctx, listAllCloudCredentials)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CloudCredential{}
	for rows.Next() {
		i, err := scanCloudCredentialRow(rows)
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

// Materializations -------------------------------------------------------

const cloudCredentialMaterializationColumns = `id, credential_id, cluster_id, namespace, secret_name, status, last_applied_at, last_error, created_at, updated_at`

func scanCloudCredentialMaterializationRow(row interface {
	Scan(dest ...any) error
}) (CloudCredentialMaterialization, error) {
	var i CloudCredentialMaterialization
	err := row.Scan(
		&i.ID,
		&i.CredentialID,
		&i.ClusterID,
		&i.Namespace,
		&i.SecretName,
		&i.Status,
		&i.LastAppliedAt,
		&i.LastError,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listCloudCredentialMaterializations = `-- name: ListCloudCredentialMaterializations :many
SELECT ` + cloudCredentialMaterializationColumns + `
FROM cloud_credential_materializations
WHERE credential_id = $1
ORDER BY cluster_id, namespace ASC`

func (q *Queries) ListCloudCredentialMaterializations(ctx context.Context, credentialID uuid.UUID) ([]CloudCredentialMaterialization, error) {
	rows, err := q.db.Query(ctx, listCloudCredentialMaterializations, credentialID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CloudCredentialMaterialization{}
	for rows.Next() {
		i, err := scanCloudCredentialMaterializationRow(rows)
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

const upsertCloudCredentialMaterialization = `-- name: UpsertCloudCredentialMaterialization :one
INSERT INTO cloud_credential_materializations (
    credential_id, cluster_id, namespace, secret_name, status
) VALUES ($1, $2, $3, $4, 'pending')
ON CONFLICT (credential_id, cluster_id, namespace) DO UPDATE SET
    secret_name = EXCLUDED.secret_name,
    status      = CASE
        WHEN cloud_credential_materializations.secret_name = EXCLUDED.secret_name
            THEN cloud_credential_materializations.status
        ELSE 'pending'
    END,
    updated_at  = now()
RETURNING ` + cloudCredentialMaterializationColumns

type UpsertCloudCredentialMaterializationParams struct {
	CredentialID uuid.UUID `json:"credential_id"`
	ClusterID    uuid.UUID `json:"cluster_id"`
	Namespace    string    `json:"namespace"`
	SecretName   string    `json:"secret_name"`
}

func (q *Queries) UpsertCloudCredentialMaterialization(ctx context.Context, arg UpsertCloudCredentialMaterializationParams) (CloudCredentialMaterialization, error) {
	row := q.db.QueryRow(ctx, upsertCloudCredentialMaterialization,
		arg.CredentialID,
		arg.ClusterID,
		arg.Namespace,
		arg.SecretName,
	)
	return scanCloudCredentialMaterializationRow(row)
}

const deleteCloudCredentialMaterialization = `-- name: DeleteCloudCredentialMaterialization :exec
DELETE FROM cloud_credential_materializations
WHERE credential_id = $1 AND cluster_id = $2 AND namespace = $3`

type DeleteCloudCredentialMaterializationParams struct {
	CredentialID uuid.UUID `json:"credential_id"`
	ClusterID    uuid.UUID `json:"cluster_id"`
	Namespace    string    `json:"namespace"`
}

func (q *Queries) DeleteCloudCredentialMaterialization(ctx context.Context, arg DeleteCloudCredentialMaterializationParams) error {
	_, err := q.db.Exec(ctx, deleteCloudCredentialMaterialization, arg.CredentialID, arg.ClusterID, arg.Namespace)
	return err
}

const deleteOrphanCloudCredentialMaterializations = `-- name: DeleteOrphanCloudCredentialMaterializations :exec
DELETE FROM cloud_credential_materializations m
WHERE m.credential_id = $1
  AND NOT EXISTS (
      SELECT 1 FROM jsonb_array_elements($2::jsonb) tgt
      WHERE tgt->>'cluster_id' = m.cluster_id::text
        AND tgt->>'namespace'  = m.namespace
  )`

type DeleteOrphanCloudCredentialMaterializationsParams struct {
	CredentialID uuid.UUID       `json:"credential_id"`
	TargetRefs   json.RawMessage `json:"target_refs"`
}

func (q *Queries) DeleteOrphanCloudCredentialMaterializations(ctx context.Context, arg DeleteOrphanCloudCredentialMaterializationsParams) error {
	_, err := q.db.Exec(ctx, deleteOrphanCloudCredentialMaterializations, arg.CredentialID, arg.TargetRefs)
	return err
}

const markCloudCredentialMaterializationApplied = `-- name: MarkCloudCredentialMaterializationApplied :exec
UPDATE cloud_credential_materializations
SET status          = 'applied',
    last_applied_at = now(),
    last_error      = '',
    updated_at      = now()
WHERE id = $1`

func (q *Queries) MarkCloudCredentialMaterializationApplied(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, markCloudCredentialMaterializationApplied, id)
	return err
}

const markCloudCredentialMaterializationFailed = `-- name: MarkCloudCredentialMaterializationFailed :exec
UPDATE cloud_credential_materializations
SET status     = 'failed',
    last_error = $2,
    updated_at = now()
WHERE id = $1`

type MarkCloudCredentialMaterializationFailedParams struct {
	ID        uuid.UUID `json:"id"`
	LastError string    `json:"last_error"`
}

func (q *Queries) MarkCloudCredentialMaterializationFailed(ctx context.Context, arg MarkCloudCredentialMaterializationFailedParams) error {
	_, err := q.db.Exec(ctx, markCloudCredentialMaterializationFailed, arg.ID, arg.LastError)
	return err
}

const listAllPendingCloudCredentialMaterializations = `-- name: ListAllPendingCloudCredentialMaterializations :many
SELECT ` + cloudCredentialMaterializationColumns + `
FROM cloud_credential_materializations
WHERE status != 'applied'
ORDER BY credential_id, cluster_id, namespace ASC`

func (q *Queries) ListAllPendingCloudCredentialMaterializations(ctx context.Context) ([]CloudCredentialMaterialization, error) {
	rows, err := q.db.Query(ctx, listAllPendingCloudCredentialMaterializations)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CloudCredentialMaterialization{}
	for rows.Next() {
		i, err := scanCloudCredentialMaterializationRow(rows)
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
