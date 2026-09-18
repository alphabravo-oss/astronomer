-- name: CountDeliveryConfigurationTemplates :one
SELECT count(*) FROM delivery_configuration_templates WHERE project_id = $1;

-- name: ListDeliveryConfigurationTemplates :many
SELECT * FROM delivery_configuration_templates
WHERE project_id = $1
ORDER BY updated_at DESC, id
LIMIT $2 OFFSET $3;

-- name: GetDeliveryConfigurationTemplate :one
SELECT * FROM delivery_configuration_templates
WHERE project_id = $1 AND id = $2;

-- name: CreateDeliveryConfigurationTemplate :one
INSERT INTO delivery_configuration_templates
    (project_id, name, description, renderer, values_document, patches, secret_refs, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- name: UpdateDeliveryConfigurationTemplate :one
UPDATE delivery_configuration_templates
SET name = $3,
    description = $4,
    renderer = $5,
    values_document = $6,
    patches = $7,
    secret_refs = $8,
    generation = generation + 1,
    updated_by = $9,
    updated_at = now()
WHERE project_id = $1 AND id = $2 AND generation = $10
RETURNING *;

-- name: DeleteDeliveryConfigurationTemplate :one
DELETE FROM delivery_configuration_templates
WHERE project_id = $1 AND id = $2 AND generation = $3
RETURNING id;
