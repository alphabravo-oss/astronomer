package sqlc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type AgentLifecycleOperation struct {
	ID             uuid.UUID          `json:"id"`
	ClusterID      uuid.UUID          `json:"cluster_id"`
	OperationType  string             `json:"operation_type"`
	Status         string             `json:"status"`
	TargetVersion  string             `json:"target_version"`
	TargetImage    string             `json:"target_image"`
	CurrentVersion string             `json:"current_version"`
	Strategy       string             `json:"strategy"`
	OperationSpec  json.RawMessage    `json:"operation_spec"`
	RequestedBy    pgtype.UUID        `json:"requested_by"`
	StartedAt      pgtype.Timestamptz `json:"started_at"`
	CompletedAt    pgtype.Timestamptz `json:"completed_at"`
	LastError      string             `json:"last_error"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

type CreateAgentLifecycleOperationParams struct {
	ClusterID      uuid.UUID       `json:"cluster_id"`
	OperationType  string          `json:"operation_type"`
	TargetVersion  string          `json:"target_version"`
	TargetImage    string          `json:"target_image"`
	CurrentVersion string          `json:"current_version"`
	Strategy       string          `json:"strategy"`
	OperationSpec  json.RawMessage `json:"operation_spec"`
	RequestedBy    pgtype.UUID     `json:"requested_by"`
}

type ListAgentLifecycleOperationsByClusterParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Limit     int32     `json:"limit"`
	Offset    int32     `json:"offset"`
}

func scanAgentLifecycleOperation(row pgx.Row) (AgentLifecycleOperation, error) {
	var i AgentLifecycleOperation
	err := row.Scan(
		&i.ID,
		&i.ClusterID,
		&i.OperationType,
		&i.Status,
		&i.TargetVersion,
		&i.TargetImage,
		&i.CurrentVersion,
		&i.Strategy,
		&i.OperationSpec,
		&i.RequestedBy,
		&i.StartedAt,
		&i.CompletedAt,
		&i.LastError,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const agentLifecycleOperationColumns = `
    id,
    cluster_id,
    operation_type,
    status,
    target_version,
    target_image,
    current_version,
    strategy,
    operation_spec,
    requested_by,
    started_at,
    completed_at,
    last_error,
    created_at,
    updated_at`

const createAgentLifecycleOperation = `-- name: CreateAgentLifecycleOperation :one
INSERT INTO agent_lifecycle_operations (
    cluster_id,
    operation_type,
    target_version,
    target_image,
    current_version,
    strategy,
    operation_spec,
    requested_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING ` + agentLifecycleOperationColumns

func (q *Queries) CreateAgentLifecycleOperation(ctx context.Context, arg CreateAgentLifecycleOperationParams) (AgentLifecycleOperation, error) {
	row := q.db.QueryRow(ctx, createAgentLifecycleOperation,
		arg.ClusterID,
		arg.OperationType,
		arg.TargetVersion,
		arg.TargetImage,
		arg.CurrentVersion,
		arg.Strategy,
		arg.OperationSpec,
		arg.RequestedBy,
	)
	return scanAgentLifecycleOperation(row)
}

const listAgentLifecycleOperationsByCluster = `-- name: ListAgentLifecycleOperationsByCluster :many
SELECT ` + agentLifecycleOperationColumns + `
FROM agent_lifecycle_operations
WHERE cluster_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3`

func (q *Queries) ListAgentLifecycleOperationsByCluster(ctx context.Context, arg ListAgentLifecycleOperationsByClusterParams) ([]AgentLifecycleOperation, error) {
	rows, err := q.db.Query(ctx, listAgentLifecycleOperationsByCluster, arg.ClusterID, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AgentLifecycleOperation{}
	for rows.Next() {
		item, err := scanAgentLifecycleOperation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const claimPendingAgentLifecycleOperation = `-- name: ClaimPendingAgentLifecycleOperation :one
WITH next_operation AS (
    SELECT id
    FROM agent_lifecycle_operations
    WHERE cluster_id = $1
      AND (
        status = 'pending'
        OR (status = 'running' AND updated_at < now() - interval '5 minutes')
      )
    ORDER BY created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE agent_lifecycle_operations
SET status = 'running',
    started_at = COALESCE(started_at, now()),
    updated_at = now(),
    last_error = ''
WHERE id IN (SELECT id FROM next_operation)
RETURNING ` + agentLifecycleOperationColumns

func (q *Queries) ClaimPendingAgentLifecycleOperation(ctx context.Context, clusterID uuid.UUID) (AgentLifecycleOperation, error) {
	row := q.db.QueryRow(ctx, claimPendingAgentLifecycleOperation, clusterID)
	return scanAgentLifecycleOperation(row)
}

type CompleteAgentLifecycleOperationParams struct {
	ID        uuid.UUID `json:"id"`
	Status    string    `json:"status"`
	LastError string    `json:"last_error"`
}

const completeAgentLifecycleOperation = `-- name: CompleteAgentLifecycleOperation :one
UPDATE agent_lifecycle_operations
SET status = $2,
    completed_at = CASE
        WHEN $2 IN ('succeeded', 'failed', 'cancelled') THEN COALESCE(completed_at, now())
        ELSE completed_at
    END,
    last_error = $3,
    updated_at = now()
WHERE id = $1
RETURNING ` + agentLifecycleOperationColumns

func (q *Queries) CompleteAgentLifecycleOperation(ctx context.Context, arg CompleteAgentLifecycleOperationParams) (AgentLifecycleOperation, error) {
	row := q.db.QueryRow(ctx, completeAgentLifecycleOperation, arg.ID, arg.Status, arg.LastError)
	return scanAgentLifecycleOperation(row)
}

type MarkRunningAgentUpgradeSucceededByVersionParams struct {
	ClusterID     uuid.UUID `json:"cluster_id"`
	TargetVersion string    `json:"target_version"`
}

const markRunningAgentUpgradeSucceededByVersion = `-- name: MarkRunningAgentUpgradeSucceededByVersion :execrows
UPDATE agent_lifecycle_operations
SET status = 'succeeded',
    completed_at = COALESCE(completed_at, now()),
    last_error = '',
    updated_at = now()
WHERE cluster_id = $1
  AND operation_type = 'agent_upgrade'
  AND status = 'running'
  AND target_version = $2`

func (q *Queries) MarkRunningAgentUpgradeSucceededByVersion(ctx context.Context, arg MarkRunningAgentUpgradeSucceededByVersionParams) (int64, error) {
	result, err := q.db.Exec(ctx, markRunningAgentUpgradeSucceededByVersion, arg.ClusterID, arg.TargetVersion)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
