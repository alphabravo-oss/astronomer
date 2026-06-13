-- Ownership metadata helpers for rows that can be created or reconciled from
-- REST/UI, CRDs, system loops, or ArgoCD. The hand-written Go shim lives in
-- internal/db/sqlc/fleet_ownership.sql.go until sqlc is runnable everywhere.

-- name: GetClusterOwnership :one
SELECT
    id,
    managed_by,
    external_ref_api_version,
    external_ref_kind,
    external_ref_namespace,
    external_ref_name,
    observed_generation
FROM clusters
WHERE id = $1;

-- name: SetClusterOwnership :one
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
    observed_generation;

-- name: GetProjectOwnership :one
SELECT
    id,
    managed_by,
    external_ref_api_version,
    external_ref_kind,
    external_ref_namespace,
    external_ref_name,
    observed_generation
FROM projects
WHERE id = $1;

-- name: SetProjectOwnership :one
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
    observed_generation;

-- name: ListCRDOwnedClusters :many
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
LIMIT $1;
