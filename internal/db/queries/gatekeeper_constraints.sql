-- P-04 Custom Gatekeeper constraint authoring (migration 133).

-- name: ListAuthoredConstraintsForCluster :many
SELECT * FROM authored_constraints
WHERE cluster_id = $1
  AND NOT (desired_state = 'absent' AND sync_status = 'synced')
ORDER BY created_at DESC;

-- name: GetAuthoredConstraintByName :one
SELECT * FROM authored_constraints
WHERE cluster_id = $1 AND name = $2
LIMIT 1;

-- name: GetAuthoredConstraintByNameForUpdate :one
SELECT * FROM authored_constraints
WHERE cluster_id = $1 AND name = $2
LIMIT 1
FOR UPDATE;

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
    created_by = EXCLUDED.created_by,
    desired_state = 'present',
    sync_status = 'pending',
    generation = authored_constraints.generation + 1,
    last_error = '',
    updated_at = now()
RETURNING *;

-- name: MarkAuthoredConstraintDeleted :one
UPDATE authored_constraints
SET desired_state = 'absent',
    sync_status = 'pending',
    generation = generation + 1,
    last_error = '',
    updated_at = now()
WHERE cluster_id = $1 AND name = $2
RETURNING *;

-- name: DeleteAuthoredConstraint :exec
DELETE FROM authored_constraints
WHERE cluster_id = $1 AND name = $2;

-- name: MarkAuthoredConstraintReconcileResult :execrows
UPDATE authored_constraints
SET sync_status = sqlc.arg(sync_status)::text,
    observed_generation = CASE WHEN sqlc.arg(sync_status)::text = 'synced' THEN sqlc.arg(observed_generation) ELSE observed_generation END,
    last_error = sqlc.arg(last_error),
    last_reconciled_at = now(),
    updated_at = now()
WHERE cluster_id = sqlc.arg(cluster_id)
  AND name = sqlc.arg(name)
  AND generation = sqlc.arg(observed_generation);

-- name: ListRecoverableAuthoredConstraints :many
SELECT * FROM authored_constraints
WHERE sync_status = 'pending'
   OR (
       sync_status = 'failed'
       AND COALESCE(last_reconciled_at, updated_at) <= now() - interval '5 minutes'
   )
ORDER BY updated_at ASC, cluster_id ASC, name ASC
LIMIT $1;
