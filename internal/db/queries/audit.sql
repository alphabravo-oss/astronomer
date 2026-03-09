-- name: GetAuditLogByID :one
SELECT * FROM audit_logs WHERE id = $1;

-- name: ListAuditLogs :many
SELECT * FROM audit_logs ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListAuditLogsByUser :many
SELECT * FROM audit_logs WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListAuditLogsByResource :many
SELECT * FROM audit_logs WHERE resource_type = $1 AND resource_id = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4;

-- name: ListAuditLogsByResourceType :many
SELECT * FROM audit_logs WHERE resource_type = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListAuditLogsByAction :many
SELECT * FROM audit_logs WHERE action = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListAuditLogsByRequestID :many
SELECT * FROM audit_logs WHERE request_id = $1 ORDER BY created_at ASC;

-- name: CreateAuditLog :one
INSERT INTO audit_logs (user_id, action, resource_type, resource_id, resource_name, detail, ip_address, user_agent, request_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: DeleteAuditLog :exec
DELETE FROM audit_logs WHERE id = $1;

-- name: CountAuditLogs :one
SELECT count(*) FROM audit_logs;

-- name: CountAuditLogsByUser :one
SELECT count(*) FROM audit_logs WHERE user_id = $1;
