package sqlc

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"
)

const operationIdempotencyClaimCTE = `
claimed AS (
    INSERT INTO operation_idempotency_keys (scope, idempotency_key)
    VALUES ($1, $2)
    ON CONFLICT (scope, idempotency_key) DO UPDATE
    SET operation_table = CASE WHEN operation_table = '' THEN $9 ELSE operation_table END,
        operation_id = COALESCE(operation_id, gen_random_uuid()),
        updated_at = now()
    RETURNING operation_table, operation_id
)`

const operationCoreColumns = `
    id, target_type, target_key, operation_type, payload, status,
    attempt_count, started_at, completed_at, error_message, created_by_id,
    created_at, updated_at`

type idempotentOperationParams struct {
	Scope          string          `json:"scope"`
	IdempotencyKey string          `json:"idempotency_key"`
	TargetType     string          `json:"target_type"`
	TargetKey      string          `json:"target_key"`
	OperationType  string          `json:"operation_type"`
	Payload        json.RawMessage `json:"payload"`
	Status         string          `json:"status"`
	CreatedByID    pgtype.UUID     `json:"created_by_id"`
}

type CreateToolOperationIdempotentParams idempotentOperationParams
type CreateCatalogOperationIdempotentParams idempotentOperationParams
type CreateLoggingOperationIdempotentParams idempotentOperationParams
type CreateWorkloadOperationIdempotentParams idempotentOperationParams
type CreateMonitoringOperationIdempotentParams idempotentOperationParams
type CreateArgoCDOperationIdempotentParams idempotentOperationParams

const createToolOperationIdempotent = `-- name: CreateToolOperationIdempotent :one
WITH ` + operationIdempotencyClaimCTE + `,
inserted AS (
    INSERT INTO tool_operations (id, target_type, target_key, operation_type, payload, status, created_by_id)
    SELECT operation_id, $3, $4, $5, $6, $7, $8
    FROM claimed
    WHERE operation_table = 'tool_operations'
    ON CONFLICT (id) DO NOTHING
    RETURNING ` + operationCoreColumns + `
),
attached AS (
    UPDATE operation_idempotency_keys
    SET response = COALESCE((SELECT to_jsonb(inserted) FROM inserted LIMIT 1), response),
        updated_at = now()
    WHERE scope = $1 AND idempotency_key = $2
)
SELECT ` + operationCoreColumns + ` FROM inserted
UNION ALL
SELECT ` + operationCoreColumns + ` FROM tool_operations
JOIN claimed ON tool_operations.id = claimed.operation_id
WHERE claimed.operation_table = 'tool_operations'
LIMIT 1`

func (q *Queries) CreateToolOperationIdempotent(ctx context.Context, arg CreateToolOperationIdempotentParams) (ToolOperation, error) {
	row := q.db.QueryRow(ctx, createToolOperationIdempotent,
		arg.Scope, arg.IdempotencyKey, arg.TargetType, arg.TargetKey, arg.OperationType, arg.Payload, arg.Status, arg.CreatedByID, "tool_operations")
	return scanToolOperationForIdempotency(row)
}

const createCatalogOperationIdempotent = `-- name: CreateCatalogOperationIdempotent :one
WITH ` + operationIdempotencyClaimCTE + `,
inserted AS (
    INSERT INTO catalog_operations (id, target_type, target_key, operation_type, payload, status, created_by_id)
    SELECT operation_id, $3, $4, $5, $6, $7, $8
    FROM claimed
    WHERE operation_table = 'catalog_operations'
    ON CONFLICT (id) DO NOTHING
    RETURNING ` + operationCoreColumns + `
),
attached AS (
    UPDATE operation_idempotency_keys
    SET response = COALESCE((SELECT to_jsonb(inserted) FROM inserted LIMIT 1), response),
        updated_at = now()
    WHERE scope = $1 AND idempotency_key = $2
)
SELECT ` + operationCoreColumns + ` FROM inserted
UNION ALL
SELECT ` + operationCoreColumns + ` FROM catalog_operations
JOIN claimed ON catalog_operations.id = claimed.operation_id
WHERE claimed.operation_table = 'catalog_operations'
LIMIT 1`

func (q *Queries) CreateCatalogOperationIdempotent(ctx context.Context, arg CreateCatalogOperationIdempotentParams) (CatalogOperation, error) {
	row := q.db.QueryRow(ctx, createCatalogOperationIdempotent,
		arg.Scope, arg.IdempotencyKey, arg.TargetType, arg.TargetKey, arg.OperationType, arg.Payload, arg.Status, arg.CreatedByID, "catalog_operations")
	return scanCatalogOperationForIdempotency(row)
}

const createLoggingOperationIdempotent = `-- name: CreateLoggingOperationIdempotent :one
WITH ` + operationIdempotencyClaimCTE + `,
inserted AS (
    INSERT INTO logging_operations (id, target_type, target_key, operation_type, payload, status, created_by_id)
    SELECT operation_id, $3, $4, $5, $6, $7, $8
    FROM claimed
    WHERE operation_table = 'logging_operations'
    ON CONFLICT (id) DO NOTHING
    RETURNING ` + operationCoreColumns + `
),
attached AS (
    UPDATE operation_idempotency_keys
    SET response = COALESCE((SELECT to_jsonb(inserted) FROM inserted LIMIT 1), response),
        updated_at = now()
    WHERE scope = $1 AND idempotency_key = $2
)
SELECT ` + operationCoreColumns + ` FROM inserted
UNION ALL
SELECT ` + operationCoreColumns + ` FROM logging_operations
JOIN claimed ON logging_operations.id = claimed.operation_id
WHERE claimed.operation_table = 'logging_operations'
LIMIT 1`

func (q *Queries) CreateLoggingOperationIdempotent(ctx context.Context, arg CreateLoggingOperationIdempotentParams) (LoggingOperation, error) {
	row := q.db.QueryRow(ctx, createLoggingOperationIdempotent,
		arg.Scope, arg.IdempotencyKey, arg.TargetType, arg.TargetKey, arg.OperationType, arg.Payload, arg.Status, arg.CreatedByID, "logging_operations")
	return scanLoggingOperationForIdempotency(row)
}

const createWorkloadOperationIdempotent = `-- name: CreateWorkloadOperationIdempotent :one
WITH ` + operationIdempotencyClaimCTE + `,
inserted AS (
    INSERT INTO workload_operations (id, target_type, target_key, operation_type, payload, status, created_by_id)
    SELECT operation_id, $3, $4, $5, $6, $7, $8
    FROM claimed
    WHERE operation_table = 'workload_operations'
    ON CONFLICT (id) DO NOTHING
    RETURNING ` + operationCoreColumns + `
),
attached AS (
    UPDATE operation_idempotency_keys
    SET response = COALESCE((SELECT to_jsonb(inserted) FROM inserted LIMIT 1), response),
        updated_at = now()
    WHERE scope = $1 AND idempotency_key = $2
)
SELECT ` + operationCoreColumns + ` FROM inserted
UNION ALL
SELECT ` + operationCoreColumns + ` FROM workload_operations
JOIN claimed ON workload_operations.id = claimed.operation_id
WHERE claimed.operation_table = 'workload_operations'
LIMIT 1`

func (q *Queries) CreateWorkloadOperationIdempotent(ctx context.Context, arg CreateWorkloadOperationIdempotentParams) (WorkloadOperation, error) {
	row := q.db.QueryRow(ctx, createWorkloadOperationIdempotent,
		arg.Scope, arg.IdempotencyKey, arg.TargetType, arg.TargetKey, arg.OperationType, arg.Payload, arg.Status, arg.CreatedByID, "workload_operations")
	return scanWorkloadOperationForIdempotency(row)
}

const createMonitoringOperationIdempotent = `-- name: CreateMonitoringOperationIdempotent :one
WITH ` + operationIdempotencyClaimCTE + `,
inserted AS (
    INSERT INTO monitoring_operations (id, target_type, target_key, operation_type, payload, status, created_by_id)
    SELECT operation_id, $3, $4, $5, $6, $7, $8
    FROM claimed
    WHERE operation_table = 'monitoring_operations'
    ON CONFLICT (id) DO NOTHING
    RETURNING ` + operationCoreColumns + `
),
attached AS (
    UPDATE operation_idempotency_keys
    SET response = COALESCE((SELECT to_jsonb(inserted) FROM inserted LIMIT 1), response),
        updated_at = now()
    WHERE scope = $1 AND idempotency_key = $2
)
SELECT ` + operationCoreColumns + ` FROM inserted
UNION ALL
SELECT ` + operationCoreColumns + ` FROM monitoring_operations
JOIN claimed ON monitoring_operations.id = claimed.operation_id
WHERE claimed.operation_table = 'monitoring_operations'
LIMIT 1`

func (q *Queries) CreateMonitoringOperationIdempotent(ctx context.Context, arg CreateMonitoringOperationIdempotentParams) (MonitoringOperation, error) {
	row := q.db.QueryRow(ctx, createMonitoringOperationIdempotent,
		arg.Scope, arg.IdempotencyKey, arg.TargetType, arg.TargetKey, arg.OperationType, arg.Payload, arg.Status, arg.CreatedByID, "monitoring_operations")
	return scanMonitoringOperationForIdempotency(row)
}

const createArgoCDOperationIdempotent = `-- name: CreateArgoCDOperationIdempotent :one
WITH ` + operationIdempotencyClaimCTE + `,
inserted AS (
    INSERT INTO argocd_operations (id, target_type, target_key, operation_type, payload, status, created_by_id)
    SELECT operation_id, $3, $4, $5, $6, $7, $8
    FROM claimed
    WHERE operation_table = 'argocd_operations'
    ON CONFLICT (id) DO NOTHING
    RETURNING id, target_type, target_key, operation_type, payload, status,
        attempt_count, started_at, completed_at, error_message, created_by_id,
        created_at, updated_at, revision, message, operation_id, phase,
        poll_attempts, last_polled_at
),
attached AS (
    UPDATE operation_idempotency_keys
    SET response = COALESCE((SELECT to_jsonb(inserted) FROM inserted LIMIT 1), response),
        updated_at = now()
    WHERE scope = $1 AND idempotency_key = $2
)
SELECT id, target_type, target_key, operation_type, payload, status,
    attempt_count, started_at, completed_at, error_message, created_by_id,
    created_at, updated_at, revision, message, operation_id, phase,
    poll_attempts, last_polled_at
FROM inserted
UNION ALL
SELECT argocd_operations.id, argocd_operations.target_type, argocd_operations.target_key,
    argocd_operations.operation_type, argocd_operations.payload, argocd_operations.status,
    argocd_operations.attempt_count, argocd_operations.started_at, argocd_operations.completed_at,
    argocd_operations.error_message, argocd_operations.created_by_id, argocd_operations.created_at,
    argocd_operations.updated_at, argocd_operations.revision, argocd_operations.message,
    argocd_operations.operation_id, argocd_operations.phase, argocd_operations.poll_attempts,
    argocd_operations.last_polled_at
FROM argocd_operations
JOIN claimed ON argocd_operations.id = claimed.operation_id
WHERE claimed.operation_table = 'argocd_operations'
LIMIT 1`

func (q *Queries) CreateArgoCDOperationIdempotent(ctx context.Context, arg CreateArgoCDOperationIdempotentParams) (ArgocdOperation, error) {
	row := q.db.QueryRow(ctx, createArgoCDOperationIdempotent,
		arg.Scope, arg.IdempotencyKey, arg.TargetType, arg.TargetKey, arg.OperationType, arg.Payload, arg.Status, arg.CreatedByID, "argocd_operations")
	return scanArgoCDOperationForIdempotency(row)
}

type operationScanRow interface {
	Scan(dest ...any) error
}

func scanToolOperationForIdempotency(row operationScanRow) (ToolOperation, error) {
	var i ToolOperation
	err := row.Scan(&i.ID, &i.TargetType, &i.TargetKey, &i.OperationType, &i.Payload, &i.Status, &i.AttemptCount, &i.StartedAt, &i.CompletedAt, &i.ErrorMessage, &i.CreatedByID, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func scanCatalogOperationForIdempotency(row operationScanRow) (CatalogOperation, error) {
	var i CatalogOperation
	err := row.Scan(&i.ID, &i.TargetType, &i.TargetKey, &i.OperationType, &i.Payload, &i.Status, &i.AttemptCount, &i.StartedAt, &i.CompletedAt, &i.ErrorMessage, &i.CreatedByID, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func scanLoggingOperationForIdempotency(row operationScanRow) (LoggingOperation, error) {
	var i LoggingOperation
	err := row.Scan(&i.ID, &i.TargetType, &i.TargetKey, &i.OperationType, &i.Payload, &i.Status, &i.AttemptCount, &i.StartedAt, &i.CompletedAt, &i.ErrorMessage, &i.CreatedByID, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func scanWorkloadOperationForIdempotency(row operationScanRow) (WorkloadOperation, error) {
	var i WorkloadOperation
	err := row.Scan(&i.ID, &i.TargetType, &i.TargetKey, &i.OperationType, &i.Payload, &i.Status, &i.AttemptCount, &i.StartedAt, &i.CompletedAt, &i.ErrorMessage, &i.CreatedByID, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func scanMonitoringOperationForIdempotency(row operationScanRow) (MonitoringOperation, error) {
	var i MonitoringOperation
	err := row.Scan(&i.ID, &i.TargetType, &i.TargetKey, &i.OperationType, &i.Payload, &i.Status, &i.AttemptCount, &i.StartedAt, &i.CompletedAt, &i.ErrorMessage, &i.CreatedByID, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func scanArgoCDOperationForIdempotency(row operationScanRow) (ArgocdOperation, error) {
	var i ArgocdOperation
	err := row.Scan(
		&i.ID, &i.TargetType, &i.TargetKey, &i.OperationType, &i.Payload, &i.Status,
		&i.AttemptCount, &i.StartedAt, &i.CompletedAt, &i.ErrorMessage, &i.CreatedByID,
		&i.CreatedAt, &i.UpdatedAt, &i.Revision, &i.Message, &i.OperationID,
		&i.Phase, &i.PollAttempts, &i.LastPolledAt,
	)
	return i, err
}
