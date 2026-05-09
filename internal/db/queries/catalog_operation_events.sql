-- name: CreateCatalogOperationEvent :one
INSERT INTO catalog_operation_events (
    operation_id,
    level,
    stage,
    message,
    detail
)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListCatalogOperationEvents :many
SELECT * FROM catalog_operation_events
WHERE operation_id = $1
ORDER BY created_at ASC;
