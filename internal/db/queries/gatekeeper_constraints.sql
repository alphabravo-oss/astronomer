-- P-04 Custom Gatekeeper constraint authoring (migration 133).

-- name: ListAuthoredConstraintsForCluster :many
SELECT * FROM authored_constraints
WHERE cluster_id = $1
ORDER BY created_at DESC;

-- name: GetAuthoredConstraintByName :one
SELECT * FROM authored_constraints
WHERE cluster_id = $1 AND name = $2
LIMIT 1;

-- name: UpsertAuthoredConstraint :one
INSERT INTO authored_constraints (
    cluster_id,
    name,
    kind,
    api_version,
    yaml,
    created_by
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (cluster_id, name) DO UPDATE SET
    kind = EXCLUDED.kind,
    api_version = EXCLUDED.api_version,
    yaml = EXCLUDED.yaml,
    updated_at = now()
RETURNING *;

-- name: DeleteAuthoredConstraint :exec
DELETE FROM authored_constraints
WHERE cluster_id = $1 AND name = $2;
