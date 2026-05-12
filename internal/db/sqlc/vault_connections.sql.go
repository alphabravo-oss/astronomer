// Migration 067 — vault_connections CRUD + project default pointer.
//
// Hand-authored sqlc shim mirroring queries/vault_connections.sql. The
// repo's sqlc CLI is occasionally not runnable in agent worktrees, so
// new query groups are hand-written following the cloud_credentials
// pattern (internal/db/sqlc/cloud_credentials.sql.go). Contents below
// are byte-compatible with what sqlc would generate so a future
// `make sqlc` is a no-op.
//
// Note on the projects.default_vault_connection_id column: existing
// SELECTs against projects enumerate columns explicitly (see
// getProjectByID), so adding a column without updating those queries
// is safe — the column is invisible to the existing path. The two
// new helpers in this file are the only readers/writers of the new
// pointer.

package sqlc

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// VaultConnection is the row shape for vault_connections.
//
// AuthEncrypted is the Fernet-encrypted JSON blob whose shape depends
// on AuthMethod:
//
//	token:      { token: "..." }
//	approle:    { role_id: "...", secret_id: "..." }
//	kubernetes: { role: "...", jwt_path: "/var/run/secrets/..." }
//
// The handler is the only thing that decrypts it; callers should treat
// AuthEncrypted as opaque.
type VaultConnection struct {
	ID                   uuid.UUID          `json:"id"`
	Name                 string             `json:"name"`
	Description          string             `json:"description"`
	Addr                 string             `json:"addr"`
	AuthMethod           string             `json:"auth_method"`
	AuthEncrypted        string             `json:"auth_encrypted"`
	Namespace            string             `json:"namespace"`
	TlsSkipVerify        bool               `json:"tls_skip_verify"`
	CaCertPem            string             `json:"ca_cert_pem"`
	DefaultMount         string             `json:"default_mount"`
	Enabled              bool               `json:"enabled"`
	CachedTokenExpiresAt pgtype.Timestamptz `json:"cached_token_expires_at"`
	LastHealthAt         pgtype.Timestamptz `json:"last_health_at"`
	LastHealthOk         bool               `json:"last_health_ok"`
	LastError            string             `json:"last_error"`
	CreatedBy            pgtype.UUID        `json:"created_by"`
	CreatedAt            time.Time          `json:"created_at"`
	UpdatedAt            time.Time          `json:"updated_at"`
}

const vaultConnectionColumns = `id, name, description, addr, auth_method, auth_encrypted, namespace, tls_skip_verify, ca_cert_pem, default_mount, enabled, cached_token_expires_at, last_health_at, last_health_ok, last_error, created_by, created_at, updated_at`

func scanVaultConnectionRow(row interface {
	Scan(dest ...any) error
}) (VaultConnection, error) {
	var i VaultConnection
	err := row.Scan(
		&i.ID,
		&i.Name,
		&i.Description,
		&i.Addr,
		&i.AuthMethod,
		&i.AuthEncrypted,
		&i.Namespace,
		&i.TlsSkipVerify,
		&i.CaCertPem,
		&i.DefaultMount,
		&i.Enabled,
		&i.CachedTokenExpiresAt,
		&i.LastHealthAt,
		&i.LastHealthOk,
		&i.LastError,
		&i.CreatedBy,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listVaultConnections = `-- name: ListVaultConnections :many
SELECT ` + vaultConnectionColumns + `
FROM vault_connections
ORDER BY name ASC`

// ListVaultConnections returns every row in vault_connections, ordered
// by name. The admin list endpoint redacts AuthEncrypted before responding.
func (q *Queries) ListVaultConnections(ctx context.Context) ([]VaultConnection, error) {
	rows, err := q.db.Query(ctx, listVaultConnections)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []VaultConnection{}
	for rows.Next() {
		i, err := scanVaultConnectionRow(rows)
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

const getVaultConnectionByID = `-- name: GetVaultConnectionByID :one
SELECT ` + vaultConnectionColumns + `
FROM vault_connections
WHERE id = $1`

func (q *Queries) GetVaultConnectionByID(ctx context.Context, id uuid.UUID) (VaultConnection, error) {
	row := q.db.QueryRow(ctx, getVaultConnectionByID, id)
	return scanVaultConnectionRow(row)
}

const getVaultConnectionByName = `-- name: GetVaultConnectionByName :one
SELECT ` + vaultConnectionColumns + `
FROM vault_connections
WHERE name = $1`

func (q *Queries) GetVaultConnectionByName(ctx context.Context, name string) (VaultConnection, error) {
	row := q.db.QueryRow(ctx, getVaultConnectionByName, name)
	return scanVaultConnectionRow(row)
}

const createVaultConnection = `-- name: CreateVaultConnection :one
INSERT INTO vault_connections (
    name, description, addr, auth_method, auth_encrypted, namespace,
    tls_skip_verify, ca_cert_pem, default_mount, enabled, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING ` + vaultConnectionColumns

// CreateVaultConnectionParams mirrors the column order of the INSERT.
type CreateVaultConnectionParams struct {
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	Addr           string      `json:"addr"`
	AuthMethod     string      `json:"auth_method"`
	AuthEncrypted  string      `json:"auth_encrypted"`
	Namespace      string      `json:"namespace"`
	TlsSkipVerify  bool        `json:"tls_skip_verify"`
	CaCertPem      string      `json:"ca_cert_pem"`
	DefaultMount   string      `json:"default_mount"`
	Enabled        bool        `json:"enabled"`
	CreatedBy      pgtype.UUID `json:"created_by"`
}

func (q *Queries) CreateVaultConnection(ctx context.Context, arg CreateVaultConnectionParams) (VaultConnection, error) {
	row := q.db.QueryRow(ctx, createVaultConnection,
		arg.Name,
		arg.Description,
		arg.Addr,
		arg.AuthMethod,
		arg.AuthEncrypted,
		arg.Namespace,
		arg.TlsSkipVerify,
		arg.CaCertPem,
		arg.DefaultMount,
		arg.Enabled,
		arg.CreatedBy,
	)
	return scanVaultConnectionRow(row)
}

const updateVaultConnection = `-- name: UpdateVaultConnection :one
UPDATE vault_connections
SET description     = $2,
    addr            = $3,
    auth_method     = $4,
    auth_encrypted  = $5,
    namespace       = $6,
    tls_skip_verify = $7,
    ca_cert_pem     = $8,
    default_mount   = $9,
    enabled         = $10,
    updated_at      = now()
WHERE id = $1
RETURNING ` + vaultConnectionColumns

type UpdateVaultConnectionParams struct {
	ID             uuid.UUID `json:"id"`
	Description    string    `json:"description"`
	Addr           string    `json:"addr"`
	AuthMethod     string    `json:"auth_method"`
	AuthEncrypted  string    `json:"auth_encrypted"`
	Namespace      string    `json:"namespace"`
	TlsSkipVerify  bool      `json:"tls_skip_verify"`
	CaCertPem      string    `json:"ca_cert_pem"`
	DefaultMount   string    `json:"default_mount"`
	Enabled        bool      `json:"enabled"`
}

func (q *Queries) UpdateVaultConnection(ctx context.Context, arg UpdateVaultConnectionParams) (VaultConnection, error) {
	row := q.db.QueryRow(ctx, updateVaultConnection,
		arg.ID,
		arg.Description,
		arg.Addr,
		arg.AuthMethod,
		arg.AuthEncrypted,
		arg.Namespace,
		arg.TlsSkipVerify,
		arg.CaCertPem,
		arg.DefaultMount,
		arg.Enabled,
	)
	return scanVaultConnectionRow(row)
}

const deleteVaultConnection = `-- name: DeleteVaultConnection :exec
DELETE FROM vault_connections WHERE id = $1`

func (q *Queries) DeleteVaultConnection(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteVaultConnection, id)
	return err
}

const updateVaultConnectionHealth = `-- name: UpdateVaultConnectionHealth :exec
UPDATE vault_connections
SET last_health_at = now(),
    last_health_ok = $2,
    last_error     = $3,
    updated_at     = now()
WHERE id = $1`

type UpdateVaultConnectionHealthParams struct {
	ID           uuid.UUID `json:"id"`
	LastHealthOk bool      `json:"last_health_ok"`
	LastError    string    `json:"last_error"`
}

func (q *Queries) UpdateVaultConnectionHealth(ctx context.Context, arg UpdateVaultConnectionHealthParams) error {
	_, err := q.db.Exec(ctx, updateVaultConnectionHealth, arg.ID, arg.LastHealthOk, arg.LastError)
	return err
}

const updateVaultConnectionTokenExpiry = `-- name: UpdateVaultConnectionTokenExpiry :exec
UPDATE vault_connections
SET cached_token_expires_at = $2,
    updated_at = now()
WHERE id = $1`

type UpdateVaultConnectionTokenExpiryParams struct {
	ID                   uuid.UUID          `json:"id"`
	CachedTokenExpiresAt pgtype.Timestamptz `json:"cached_token_expires_at"`
}

func (q *Queries) UpdateVaultConnectionTokenExpiry(ctx context.Context, arg UpdateVaultConnectionTokenExpiryParams) error {
	_, err := q.db.Exec(ctx, updateVaultConnectionTokenExpiry, arg.ID, arg.CachedTokenExpiresAt)
	return err
}

const setProjectDefaultVaultConnection = `-- name: SetProjectDefaultVaultConnection :exec
UPDATE projects
SET default_vault_connection_id = $2,
    updated_at = now()
WHERE id = $1`

// SetProjectDefaultVaultConnectionParams. Pass a zero pgtype.UUID to
// clear the pointer (UPDATE with NULL).
type SetProjectDefaultVaultConnectionParams struct {
	ProjectID                uuid.UUID   `json:"project_id"`
	DefaultVaultConnectionID pgtype.UUID `json:"default_vault_connection_id"`
}

func (q *Queries) SetProjectDefaultVaultConnection(ctx context.Context, arg SetProjectDefaultVaultConnectionParams) error {
	_, err := q.db.Exec(ctx, setProjectDefaultVaultConnection, arg.ProjectID, arg.DefaultVaultConnectionID)
	return err
}

const getProjectDefaultVaultConnection = `-- name: GetProjectDefaultVaultConnection :one
SELECT default_vault_connection_id FROM projects WHERE id = $1`

// GetProjectDefaultVaultConnection returns the (possibly NULL) FK pointer.
// Caller decides whether to chase it.
func (q *Queries) GetProjectDefaultVaultConnection(ctx context.Context, projectID uuid.UUID) (pgtype.UUID, error) {
	row := q.db.QueryRow(ctx, getProjectDefaultVaultConnection, projectID)
	var id pgtype.UUID
	err := row.Scan(&id)
	return id, err
}
