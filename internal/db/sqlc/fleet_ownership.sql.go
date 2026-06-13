// Hand-written sqlc-style shim for fleet ownership metadata.
// The canonical SQL lives in internal/db/queries/fleet_ownership.sql.
package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type FleetOwnership struct {
	ID                    uuid.UUID `json:"id"`
	ManagedBy             string    `json:"managed_by"`
	ExternalRefApiVersion string    `json:"external_ref_api_version"`
	ExternalRefKind       string    `json:"external_ref_kind"`
	ExternalRefNamespace  string    `json:"external_ref_namespace"`
	ExternalRefName       string    `json:"external_ref_name"`
	ObservedGeneration    int64     `json:"observed_generation"`
}

type SetClusterOwnershipParams struct {
	ID                    uuid.UUID `json:"id"`
	ManagedBy             string    `json:"managed_by"`
	ExternalRefApiVersion string    `json:"external_ref_api_version"`
	ExternalRefKind       string    `json:"external_ref_kind"`
	ExternalRefNamespace  string    `json:"external_ref_namespace"`
	ExternalRefName       string    `json:"external_ref_name"`
	ObservedGeneration    int64     `json:"observed_generation"`
}

type SetProjectOwnershipParams struct {
	ID                    uuid.UUID `json:"id"`
	ManagedBy             string    `json:"managed_by"`
	ExternalRefApiVersion string    `json:"external_ref_api_version"`
	ExternalRefKind       string    `json:"external_ref_kind"`
	ExternalRefNamespace  string    `json:"external_ref_namespace"`
	ExternalRefName       string    `json:"external_ref_name"`
	ObservedGeneration    int64     `json:"observed_generation"`
}

func scanFleetOwnership(row pgx.Row) (FleetOwnership, error) {
	var i FleetOwnership
	err := row.Scan(
		&i.ID,
		&i.ManagedBy,
		&i.ExternalRefApiVersion,
		&i.ExternalRefKind,
		&i.ExternalRefNamespace,
		&i.ExternalRefName,
		&i.ObservedGeneration,
	)
	return i, err
}

const getClusterOwnership = `-- name: GetClusterOwnership :one
SELECT
    id,
    managed_by,
    external_ref_api_version,
    external_ref_kind,
    external_ref_namespace,
    external_ref_name,
    observed_generation
FROM clusters
WHERE id = $1
`

func (q *Queries) GetClusterOwnership(ctx context.Context, id uuid.UUID) (FleetOwnership, error) {
	row := q.db.QueryRow(ctx, getClusterOwnership, id)
	return scanFleetOwnership(row)
}

const setClusterOwnership = `-- name: SetClusterOwnership :one
UPDATE clusters
SET
    managed_by = $2,
    external_ref_api_version = $3,
    external_ref_kind = $4,
    external_ref_namespace = $5,
    external_ref_name = $6,
    observed_generation = $7,
    updated_at = now()
WHERE id = $1
RETURNING
    id,
    managed_by,
    external_ref_api_version,
    external_ref_kind,
    external_ref_namespace,
    external_ref_name,
    observed_generation
`

func (q *Queries) SetClusterOwnership(ctx context.Context, arg SetClusterOwnershipParams) (FleetOwnership, error) {
	row := q.db.QueryRow(ctx, setClusterOwnership,
		arg.ID,
		arg.ManagedBy,
		arg.ExternalRefApiVersion,
		arg.ExternalRefKind,
		arg.ExternalRefNamespace,
		arg.ExternalRefName,
		arg.ObservedGeneration,
	)
	return scanFleetOwnership(row)
}

const getProjectOwnership = `-- name: GetProjectOwnership :one
SELECT
    id,
    managed_by,
    external_ref_api_version,
    external_ref_kind,
    external_ref_namespace,
    external_ref_name,
    observed_generation
FROM projects
WHERE id = $1
`

func (q *Queries) GetProjectOwnership(ctx context.Context, id uuid.UUID) (FleetOwnership, error) {
	row := q.db.QueryRow(ctx, getProjectOwnership, id)
	return scanFleetOwnership(row)
}

const setProjectOwnership = `-- name: SetProjectOwnership :one
UPDATE projects
SET
    managed_by = $2,
    external_ref_api_version = $3,
    external_ref_kind = $4,
    external_ref_namespace = $5,
    external_ref_name = $6,
    observed_generation = $7,
    updated_at = now()
WHERE id = $1
RETURNING
    id,
    managed_by,
    external_ref_api_version,
    external_ref_kind,
    external_ref_namespace,
    external_ref_name,
    observed_generation
`

func (q *Queries) SetProjectOwnership(ctx context.Context, arg SetProjectOwnershipParams) (FleetOwnership, error) {
	row := q.db.QueryRow(ctx, setProjectOwnership,
		arg.ID,
		arg.ManagedBy,
		arg.ExternalRefApiVersion,
		arg.ExternalRefKind,
		arg.ExternalRefNamespace,
		arg.ExternalRefName,
		arg.ObservedGeneration,
	)
	return scanFleetOwnership(row)
}

const listCRDOwnedClusters = `-- name: ListCRDOwnedClusters :many
SELECT
    id,
    managed_by,
    external_ref_api_version,
    external_ref_kind,
    external_ref_namespace,
    external_ref_name,
    observed_generation
FROM clusters
WHERE managed_by = 'crd'
  AND external_ref_name <> ''
  AND decommissioned_at IS NULL
ORDER BY updated_at ASC
LIMIT $1
`

func (q *Queries) ListCRDOwnedClusters(ctx context.Context, limit int32) ([]FleetOwnership, error) {
	rows, err := q.db.Query(ctx, listCRDOwnedClusters, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []FleetOwnership{}
	for rows.Next() {
		var i FleetOwnership
		if err := rows.Scan(
			&i.ID,
			&i.ManagedBy,
			&i.ExternalRefApiVersion,
			&i.ExternalRefKind,
			&i.ExternalRefNamespace,
			&i.ExternalRefName,
			&i.ObservedGeneration,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
