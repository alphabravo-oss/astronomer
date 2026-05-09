-- name: CreateMonitoringOperationEvent :one
INSERT INTO monitoring_operation_events (
    operation_id,
    level,
    stage,
    message,
    detail
)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListMonitoringOperationEvents :many
SELECT * FROM monitoring_operation_events
WHERE operation_id = $1
ORDER BY created_at ASC;
