// Hand-written sqlc-style shim for ArgoCD cluster-proxy service tokens.
// See cluster_registration.sql.go for why this repo carries a few manual
// query shims alongside generated sqlc output.
package sqlc

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type ArgocdClusterProxyToken struct {
	ID             uuid.UUID          `json:"id"`
	ClusterID      uuid.UUID          `json:"cluster_id"`
	Purpose        string             `json:"purpose"`
	TokenHash      string             `json:"token_hash"`
	TokenPrefix    string             `json:"token_prefix"`
	TokenEncrypted string             `json:"token_encrypted"`
	ExpiresAt      pgtype.Timestamptz `json:"expires_at"`
	LastUsedAt     pgtype.Timestamptz `json:"last_used_at"`
	IsRevoked      bool               `json:"is_revoked"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

func scanArgocdClusterProxyToken(row pgx.Row) (ArgocdClusterProxyToken, error) {
	var i ArgocdClusterProxyToken
	err := row.Scan(
		&i.ID,
		&i.ClusterID,
		&i.Purpose,
		&i.TokenHash,
		&i.TokenPrefix,
		&i.TokenEncrypted,
		&i.ExpiresAt,
		&i.LastUsedAt,
		&i.IsRevoked,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const getActiveArgoCDClusterProxyTokenByClusterID = `-- name: GetActiveArgoCDClusterProxyTokenByClusterID :one
SELECT id, cluster_id, purpose, token_hash, token_prefix, token_encrypted, expires_at, last_used_at, is_revoked, created_at, updated_at
FROM argocd_cluster_proxy_tokens
WHERE cluster_id = $1
  AND purpose = 'argocd_cluster_proxy'
  AND is_revoked = false
  AND (expires_at IS NULL OR expires_at > now())
`

func (q *Queries) GetActiveArgoCDClusterProxyTokenByClusterID(ctx context.Context, clusterID uuid.UUID) (ArgocdClusterProxyToken, error) {
	row := q.db.QueryRow(ctx, getActiveArgoCDClusterProxyTokenByClusterID, clusterID)
	return scanArgocdClusterProxyToken(row)
}

const getArgoCDClusterProxyTokenByHash = `-- name: GetArgoCDClusterProxyTokenByHash :one
SELECT id, cluster_id, purpose, token_hash, token_prefix, token_encrypted, expires_at, last_used_at, is_revoked, created_at, updated_at
FROM argocd_cluster_proxy_tokens
WHERE token_hash = $1
  AND purpose = 'argocd_cluster_proxy'
  AND is_revoked = false
  AND (expires_at IS NULL OR expires_at > now())
`

func (q *Queries) GetArgoCDClusterProxyTokenByHash(ctx context.Context, tokenHash string) (ArgocdClusterProxyToken, error) {
	row := q.db.QueryRow(ctx, getArgoCDClusterProxyTokenByHash, tokenHash)
	return scanArgocdClusterProxyToken(row)
}

const upsertArgoCDClusterProxyToken = `-- name: UpsertArgoCDClusterProxyToken :one
INSERT INTO argocd_cluster_proxy_tokens
    (cluster_id, token_hash, token_prefix, token_encrypted, expires_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (cluster_id, purpose) DO UPDATE SET
    token_hash = EXCLUDED.token_hash,
    token_prefix = EXCLUDED.token_prefix,
    token_encrypted = EXCLUDED.token_encrypted,
    expires_at = EXCLUDED.expires_at,
    is_revoked = false,
    updated_at = now()
RETURNING id, cluster_id, purpose, token_hash, token_prefix, token_encrypted, expires_at, last_used_at, is_revoked, created_at, updated_at
`

type UpsertArgoCDClusterProxyTokenParams struct {
	ClusterID      uuid.UUID          `json:"cluster_id"`
	TokenHash      string             `json:"token_hash"`
	TokenPrefix    string             `json:"token_prefix"`
	TokenEncrypted string             `json:"token_encrypted"`
	ExpiresAt      pgtype.Timestamptz `json:"expires_at"`
}

func (q *Queries) UpsertArgoCDClusterProxyToken(ctx context.Context, arg UpsertArgoCDClusterProxyTokenParams) (ArgocdClusterProxyToken, error) {
	row := q.db.QueryRow(ctx, upsertArgoCDClusterProxyToken,
		arg.ClusterID,
		arg.TokenHash,
		arg.TokenPrefix,
		arg.TokenEncrypted,
		arg.ExpiresAt,
	)
	return scanArgocdClusterProxyToken(row)
}

const touchArgoCDClusterProxyToken = `-- name: TouchArgoCDClusterProxyToken :exec
UPDATE argocd_cluster_proxy_tokens SET last_used_at = now() WHERE id = $1
`

func (q *Queries) TouchArgoCDClusterProxyToken(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, touchArgoCDClusterProxyToken, id)
	return err
}

const revokeArgoCDClusterProxyTokenForCluster = `-- name: RevokeArgoCDClusterProxyTokenForCluster :exec
UPDATE argocd_cluster_proxy_tokens
SET is_revoked = true, updated_at = now()
WHERE cluster_id = $1 AND purpose = 'argocd_cluster_proxy'
`

func (q *Queries) RevokeArgoCDClusterProxyTokenForCluster(ctx context.Context, clusterID uuid.UUID) error {
	_, err := q.db.Exec(ctx, revokeArgoCDClusterProxyTokenForCluster, clusterID)
	return err
}

const deleteArgoCDClusterProxyTokensByCluster = `-- name: DeleteArgoCDClusterProxyTokensByCluster :execrows
DELETE FROM argocd_cluster_proxy_tokens WHERE cluster_id = $1
`

func (q *Queries) DeleteArgoCDClusterProxyTokensByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error) {
	result, err := q.db.Exec(ctx, deleteArgoCDClusterProxyTokensByCluster, clusterID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
