package sqlc

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"
)

const operationIdempotencyClaimCTE = `
claimed AS (
    INSERT INTO operation_idempotency_keys (scope, idempotency_key, operation_table, operation_id)
    VALUES ($1, $2, $9, gen_random_uuid())
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

const createToolOperationIdempotent = `-- name: CreateToolOperationIdempotent :one
WITH ` + operationIdempotencyClaimCTE + `,
inserted AS (
    INSERT INTO tool_operations (id, target_type, target_key, operation_type, payload, status, created_by_id)
    SELECT operation_id, $3, $4, $5, $6, $7, $8
    FROM claimed
    WHERE operation_table = 'tool_operations'
    ON CONFLICT (id) DO NOTHING
    RETURNING ` + operationCoreColumns + `
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
	op, err := scanToolOperationForIdempotency(row)
	if err == nil {
		err = q.attachOperationIdempotencyResponse(ctx, arg.Scope, arg.IdempotencyKey, "tool_operations", op.ID, op)
	}
	return op, err
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
	op, err := scanCatalogOperationForIdempotency(row)
	if err == nil {
		err = q.attachOperationIdempotencyResponse(ctx, arg.Scope, arg.IdempotencyKey, "catalog_operations", op.ID, op)
	}
	return op, err
}

// CreateCatalogOperationIdempotentWithDisposition is the mutation-service
// variant of CreateCatalogOperationIdempotent. Handlers that stage domain
// state before creating the operation must know whether this call inserted the
// operation or replayed an older one; otherwise a replay can commit newly
// staged state while returning an already-completed operation.
type CreateCatalogOperationIdempotentWithDispositionParams CreateCatalogOperationIdempotentParams

type CreateCatalogOperationIdempotentWithDispositionRow struct {
	CatalogOperation
	Inserted bool `json:"inserted"`
}

const createCatalogOperationIdempotentWithDisposition = `-- name: CreateCatalogOperationIdempotentWithDisposition :one
WITH ` + operationIdempotencyClaimCTE + `,
inserted AS (
    INSERT INTO catalog_operations (id, target_type, target_key, operation_type, payload, status, created_by_id)
    SELECT operation_id, $3, $4, $5, $6, $7, $8
    FROM claimed
    WHERE operation_table = 'catalog_operations'
    ON CONFLICT (id) DO NOTHING
    RETURNING ` + operationCoreColumns + `
)
SELECT ` + operationCoreColumns + `, true AS inserted FROM inserted
UNION ALL
SELECT ` + operationCoreColumns + `, false AS inserted FROM catalog_operations
JOIN claimed ON catalog_operations.id = claimed.operation_id
WHERE claimed.operation_table = 'catalog_operations'
LIMIT 1`

func (q *Queries) CreateCatalogOperationIdempotentWithDisposition(ctx context.Context, arg CreateCatalogOperationIdempotentWithDispositionParams) (CreateCatalogOperationIdempotentWithDispositionRow, error) {
	row := q.db.QueryRow(ctx, createCatalogOperationIdempotentWithDisposition,
		arg.Scope, arg.IdempotencyKey, arg.TargetType, arg.TargetKey, arg.OperationType, arg.Payload, arg.Status, arg.CreatedByID, "catalog_operations")
	var result CreateCatalogOperationIdempotentWithDispositionRow
	i := &result.CatalogOperation
	err := row.Scan(&i.ID, &i.TargetType, &i.TargetKey, &i.OperationType, &i.Payload, &i.Status, &i.AttemptCount, &i.StartedAt, &i.CompletedAt, &i.ErrorMessage, &i.CreatedByID, &i.CreatedAt, &i.UpdatedAt, &result.Inserted)
	if err == nil {
		err = q.attachOperationIdempotencyResponse(ctx, arg.Scope, arg.IdempotencyKey, "catalog_operations", i.ID, *i)
	}
	return result, err
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
	op, err := scanLoggingOperationForIdempotency(row)
	if err == nil {
		err = q.attachOperationIdempotencyResponse(ctx, arg.Scope, arg.IdempotencyKey, "logging_operations", op.ID, op)
	}
	return op, err
}

// CreateLoggingOperationIdempotentWithDisposition is the mutation-service
// variant of CreateLoggingOperationIdempotent. A logging handler that stages
// desired configuration in the same transaction must distinguish a newly
// inserted operation from a replay, or it could commit new configuration that
// has no corresponding executable operation.
type CreateLoggingOperationIdempotentWithDispositionParams CreateLoggingOperationIdempotentParams

type CreateLoggingOperationIdempotentWithDispositionRow struct {
	LoggingOperation
	Inserted bool `json:"inserted"`
}

const createLoggingOperationIdempotentWithDisposition = `-- name: CreateLoggingOperationIdempotentWithDisposition :one
WITH ` + operationIdempotencyClaimCTE + `,
inserted AS (
    INSERT INTO logging_operations (id, target_type, target_key, operation_type, payload, status, created_by_id)
    SELECT operation_id, $3, $4, $5, $6, $7, $8
    FROM claimed
    WHERE operation_table = 'logging_operations'
    ON CONFLICT (id) DO NOTHING
    RETURNING ` + operationCoreColumns + `
)
SELECT ` + operationCoreColumns + `, true AS inserted FROM inserted
UNION ALL
SELECT ` + operationCoreColumns + `, false AS inserted FROM logging_operations
JOIN claimed ON logging_operations.id = claimed.operation_id
WHERE claimed.operation_table = 'logging_operations'
LIMIT 1`

func (q *Queries) CreateLoggingOperationIdempotentWithDisposition(ctx context.Context, arg CreateLoggingOperationIdempotentWithDispositionParams) (CreateLoggingOperationIdempotentWithDispositionRow, error) {
	row := q.db.QueryRow(ctx, createLoggingOperationIdempotentWithDisposition,
		arg.Scope, arg.IdempotencyKey, arg.TargetType, arg.TargetKey, arg.OperationType, arg.Payload, arg.Status, arg.CreatedByID, "logging_operations")
	var result CreateLoggingOperationIdempotentWithDispositionRow
	i := &result.LoggingOperation
	err := row.Scan(&i.ID, &i.TargetType, &i.TargetKey, &i.OperationType, &i.Payload, &i.Status, &i.AttemptCount, &i.StartedAt, &i.CompletedAt, &i.ErrorMessage, &i.CreatedByID, &i.CreatedAt, &i.UpdatedAt, &result.Inserted)
	if err == nil {
		err = q.attachOperationIdempotencyResponse(ctx, arg.Scope, arg.IdempotencyKey, "logging_operations", i.ID, *i)
	}
	return result, err
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
	op, err := scanWorkloadOperationForIdempotency(row)
	if err == nil {
		err = q.attachOperationIdempotencyResponse(ctx, arg.Scope, arg.IdempotencyKey, "workload_operations", op.ID, op)
	}
	return op, err
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
	op, err := scanMonitoringOperationForIdempotency(row)
	if err == nil {
		err = q.attachOperationIdempotencyResponse(ctx, arg.Scope, arg.IdempotencyKey, "monitoring_operations", op.ID, op)
	}
	return op, err
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
