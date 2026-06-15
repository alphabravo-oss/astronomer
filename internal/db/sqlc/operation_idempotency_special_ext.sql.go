package sqlc

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type CreateFleetOperationIdempotentParams struct {
	Scope                     string          `json:"scope"`
	IdempotencyKey            string          `json:"idempotency_key"`
	Name                      string          `json:"name"`
	Description               string          `json:"description"`
	OperationType             string          `json:"operation_type"`
	OperationSpec             json.RawMessage `json:"operation_spec"`
	Selector                  json.RawMessage `json:"selector"`
	Strategy                  string          `json:"strategy"`
	MaxConcurrent             int32           `json:"max_concurrent"`
	OnError                   string          `json:"on_error"`
	RespectMaintenanceWindows bool            `json:"respect_maintenance_windows"`
	CreatedBy                 pgtype.UUID     `json:"created_by"`
}

const fleetOperationColumns = `
    id, name, description, operation_type, operation_spec, selector, strategy,
    max_concurrent, on_error, respect_maintenance_windows, status,
    total_clusters, completed_clusters, failed_clusters, skipped_clusters,
    started_at, completed_at, last_error, created_by, created_at, updated_at`

func scanFleetOperation(row operationScanRow) (FleetOperation, error) {
	var i FleetOperation
	err := row.Scan(
		&i.ID,
		&i.Name,
		&i.Description,
		&i.OperationType,
		&i.OperationSpec,
		&i.Selector,
		&i.Strategy,
		&i.MaxConcurrent,
		&i.OnError,
		&i.RespectMaintenanceWindows,
		&i.Status,
		&i.TotalClusters,
		&i.CompletedClusters,
		&i.FailedClusters,
		&i.SkippedClusters,
		&i.StartedAt,
		&i.CompletedAt,
		&i.LastError,
		&i.CreatedBy,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const createFleetOperationIdempotent = `-- name: CreateFleetOperationIdempotent :one
WITH claimed AS (
    INSERT INTO operation_idempotency_keys (scope, idempotency_key)
    VALUES ($1, $2)
    ON CONFLICT (scope, idempotency_key) DO UPDATE
    SET operation_table = CASE WHEN operation_table = '' THEN 'fleet_operations' ELSE operation_table END,
        operation_id = COALESCE(operation_id, gen_random_uuid()),
        updated_at = now()
    RETURNING operation_table, operation_id
),
inserted AS (
    INSERT INTO fleet_operations (
        id, name, description, operation_type, operation_spec, selector,
        strategy, max_concurrent, on_error, respect_maintenance_windows, created_by
    )
    SELECT operation_id, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
    FROM claimed
    WHERE operation_table = 'fleet_operations'
    ON CONFLICT (id) DO NOTHING
    RETURNING ` + fleetOperationColumns + `
),
attached AS (
    UPDATE operation_idempotency_keys
    SET response = COALESCE((SELECT to_jsonb(inserted) FROM inserted LIMIT 1), response),
        updated_at = now()
    WHERE scope = $1 AND idempotency_key = $2
)
SELECT ` + fleetOperationColumns + ` FROM inserted
UNION ALL
SELECT ` + fleetOperationColumns + `
FROM fleet_operations
JOIN claimed ON fleet_operations.id = claimed.operation_id
WHERE claimed.operation_table = 'fleet_operations'
LIMIT 1`

func (q *Queries) CreateFleetOperationIdempotent(ctx context.Context, arg CreateFleetOperationIdempotentParams) (FleetOperation, error) {
	return scanFleetOperation(q.db.QueryRow(ctx, createFleetOperationIdempotent,
		arg.Scope,
		arg.IdempotencyKey,
		arg.Name,
		arg.Description,
		arg.OperationType,
		arg.OperationSpec,
		arg.Selector,
		arg.Strategy,
		arg.MaxConcurrent,
		arg.OnError,
		arg.RespectMaintenanceWindows,
		arg.CreatedBy,
	))
}

type CreateRestoreOperationIdempotentParams struct {
	Scope              string          `json:"scope"`
	IdempotencyKey     string          `json:"idempotency_key"`
	BackupID           uuid.UUID       `json:"backup_id"`
	Status             string          `json:"status"`
	InitiatedByID      pgtype.UUID     `json:"initiated_by_id"`
	ClusterID          pgtype.UUID     `json:"cluster_id"`
	VeleroNamespace    string          `json:"velero_namespace"`
	VeleroRestoreName  string          `json:"velero_restore_name"`
	IncludedNamespaces json.RawMessage `json:"included_namespaces"`
	NamespaceMapping   json.RawMessage `json:"namespace_mapping"`
}

const restoreOperationColumns = `
    id, backup_id, status, started_at, completed_at, error_message,
    initiated_by_id, created_at, updated_at, cluster_id, velero_namespace,
    velero_restore_name, included_namespaces, namespace_mapping,
    poll_attempts, last_polled_at`

const createRestoreOperationIdempotent = `-- name: CreateRestoreOperationIdempotent :one
WITH claimed AS (
    INSERT INTO operation_idempotency_keys (scope, idempotency_key)
    VALUES ($1, $2)
    ON CONFLICT (scope, idempotency_key) DO UPDATE
    SET operation_table = CASE WHEN operation_table = '' THEN 'restore_operations' ELSE operation_table END,
        operation_id = COALESCE(operation_id, gen_random_uuid()),
        updated_at = now()
    RETURNING operation_table, operation_id
),
inserted AS (
    INSERT INTO restore_operations (
        id, backup_id, status, initiated_by_id, cluster_id, velero_namespace,
        velero_restore_name, included_namespaces, namespace_mapping
    )
    SELECT operation_id, $3, $4, $5, $6, $7, $8, $9, $10
    FROM claimed
    WHERE operation_table = 'restore_operations'
    ON CONFLICT (id) DO NOTHING
    RETURNING ` + restoreOperationColumns + `
),
attached AS (
    UPDATE operation_idempotency_keys
    SET response = COALESCE((SELECT to_jsonb(inserted) FROM inserted LIMIT 1), response),
        updated_at = now()
    WHERE scope = $1 AND idempotency_key = $2
)
SELECT ` + restoreOperationColumns + ` FROM inserted
UNION ALL
SELECT ` + restoreOperationColumns + `
FROM restore_operations
JOIN claimed ON restore_operations.id = claimed.operation_id
WHERE claimed.operation_table = 'restore_operations'
LIMIT 1`

func (q *Queries) CreateRestoreOperationIdempotent(ctx context.Context, arg CreateRestoreOperationIdempotentParams) (RestoreOperation, error) {
	return scanRestoreOperationForIdempotency(q.db.QueryRow(ctx, createRestoreOperationIdempotent,
		arg.Scope,
		arg.IdempotencyKey,
		arg.BackupID,
		arg.Status,
		arg.InitiatedByID,
		arg.ClusterID,
		arg.VeleroNamespace,
		arg.VeleroRestoreName,
		arg.IncludedNamespaces,
		arg.NamespaceMapping,
	))
}

type CreateDeferredOperationIdempotentParams struct {
	Scope           string             `json:"scope"`
	IdempotencyKey  string             `json:"idempotency_key"`
	WindowID        uuid.UUID          `json:"window_id"`
	OperationType   string             `json:"operation_type"`
	OperationSpec   json.RawMessage    `json:"operation_spec"`
	TargetClusterID pgtype.UUID        `json:"target_cluster_id"`
	TargetProjectID pgtype.UUID        `json:"target_project_id"`
	DeferredUntil   pgtype.Timestamptz `json:"deferred_until"`
	ExpiresAt       pgtype.Timestamptz `json:"expires_at"`
	RequestedBy     pgtype.UUID        `json:"requested_by"`
}

const createDeferredOperationIdempotent = `-- name: CreateDeferredOperationIdempotent :one
WITH claimed AS (
    INSERT INTO operation_idempotency_keys (scope, idempotency_key)
    VALUES ($1, $2)
    ON CONFLICT (scope, idempotency_key) DO UPDATE
    SET operation_table = CASE WHEN operation_table = '' THEN 'deferred_operations' ELSE operation_table END,
        operation_id = COALESCE(operation_id, gen_random_uuid()),
        updated_at = now()
    RETURNING operation_table, operation_id
),
inserted AS (
    INSERT INTO deferred_operations (
        id, window_id, operation_type, operation_spec, target_cluster_id,
        target_project_id, deferred_until, expires_at, requested_by
    )
    SELECT operation_id, $3, $4, $5, $6, $7, $8, $9, $10
    FROM claimed
    WHERE operation_table = 'deferred_operations'
    ON CONFLICT (id) DO NOTHING
    RETURNING ` + deferredOperationSelectColumns + `
),
attached AS (
    UPDATE operation_idempotency_keys
    SET response = COALESCE((SELECT to_jsonb(inserted) FROM inserted LIMIT 1), response),
        updated_at = now()
    WHERE scope = $1 AND idempotency_key = $2
)
SELECT ` + deferredOperationSelectColumns + ` FROM inserted
UNION ALL
SELECT ` + deferredOperationSelectColumns + `
FROM deferred_operations
JOIN claimed ON deferred_operations.id = claimed.operation_id
WHERE claimed.operation_table = 'deferred_operations'
LIMIT 1`

func (q *Queries) CreateDeferredOperationIdempotent(ctx context.Context, arg CreateDeferredOperationIdempotentParams) (DeferredOperation, error) {
	return scanDeferredOperationRow(q.db.QueryRow(ctx, createDeferredOperationIdempotent,
		arg.Scope,
		arg.IdempotencyKey,
		arg.WindowID,
		arg.OperationType,
		arg.OperationSpec,
		arg.TargetClusterID,
		arg.TargetProjectID,
		arg.DeferredUntil,
		arg.ExpiresAt,
		arg.RequestedBy,
	))
}

func scanRestoreOperationForIdempotency(row operationScanRow) (RestoreOperation, error) {
	var i RestoreOperation
	err := row.Scan(
		&i.ID,
		&i.BackupID,
		&i.Status,
		&i.StartedAt,
		&i.CompletedAt,
		&i.ErrorMessage,
		&i.InitiatedByID,
		&i.CreatedAt,
		&i.UpdatedAt,
		&i.ClusterID,
		&i.VeleroNamespace,
		&i.VeleroRestoreName,
		&i.IncludedNamespaces,
		&i.NamespaceMapping,
		&i.PollAttempts,
		&i.LastPolledAt,
	)
	return i, err
}
