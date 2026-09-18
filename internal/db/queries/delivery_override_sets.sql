-- name: CountDeliveryOverrideSets :one
SELECT count(*) FROM delivery_override_sets WHERE project_id = $1;

-- name: ListDeliveryOverrideSets :many
SELECT * FROM delivery_override_sets
WHERE project_id = $1
ORDER BY scope_type, precedence, name, id
LIMIT $2 OFFSET $3;

-- name: GetDeliveryOverrideSet :one
SELECT * FROM delivery_override_sets
WHERE project_id = $1 AND id = $2;

-- name: ListDeliveryOverrideSetsByIDs :many
SELECT * FROM delivery_override_sets
WHERE project_id = $1 AND enabled = true AND id = ANY($2::uuid[])
ORDER BY scope_type, precedence, name, id;

-- name: CreateDeliveryOverrideSet :one
INSERT INTO delivery_override_sets
    (project_id, template_id, name, scope_type, scope_id, precedence,
     values_document, patches, enabled, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
RETURNING *;

-- name: UpdateDeliveryOverrideSet :one
UPDATE delivery_override_sets
SET template_id = $3,
    name = $4,
    scope_type = $5,
    scope_id = $6,
    precedence = $7,
    values_document = $8,
    patches = $9,
    enabled = $10,
    generation = generation + 1,
    updated_by = $11,
    updated_at = now()
WHERE project_id = $1 AND id = $2 AND generation = $12
RETURNING *;

-- name: DeleteDeliveryOverrideSet :one
DELETE FROM delivery_override_sets
WHERE project_id = $1 AND id = $2 AND generation = $3
RETURNING id;
