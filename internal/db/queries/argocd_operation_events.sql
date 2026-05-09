-- name: CreateArgoCDOperationEvent :one
INSERT INTO argocd_operation_events (
    operation_id,
    level,
    stage,
    message,
    detail
)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListArgoCDOperationEvents :many
SELECT * FROM argocd_operation_events
WHERE operation_id = $1
ORDER BY created_at ASC;
