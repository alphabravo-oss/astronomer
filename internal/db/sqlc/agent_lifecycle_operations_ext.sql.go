package sqlc

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

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

type CreateAgentLifecycleOperationIdempotentParams struct {
	Scope          string          `json:"scope"`
	IdempotencyKey string          `json:"idempotency_key"`
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

const createAgentLifecycleOperationIdempotent = `-- name: CreateAgentLifecycleOperationIdempotent :one
WITH claimed AS (
    INSERT INTO operation_idempotency_keys (scope, idempotency_key)
    VALUES ($1, $2)
    ON CONFLICT (scope, idempotency_key) DO UPDATE
    SET operation_table = CASE WHEN operation_table = '' THEN 'agent_lifecycle_operations' ELSE operation_table END,
        operation_id = COALESCE(operation_id, gen_random_uuid()),
        updated_at = now()
    RETURNING operation_table, operation_id
),
inserted AS (
    INSERT INTO agent_lifecycle_operations (
        id,
        cluster_id,
        operation_type,
        target_version,
        target_image,
        current_version,
        strategy,
        operation_spec,
        requested_by
    )
    SELECT operation_id, $3, $4, $5, $6, $7, $8, $9, $10
    FROM claimed
    WHERE operation_table = 'agent_lifecycle_operations'
    ON CONFLICT (id) DO NOTHING
    RETURNING ` + agentLifecycleOperationColumns + `
),
attached AS (
    UPDATE operation_idempotency_keys
    SET response = COALESCE((SELECT to_jsonb(inserted) FROM inserted LIMIT 1), response),
        updated_at = now()
    WHERE scope = $1 AND idempotency_key = $2
)
SELECT ` + agentLifecycleOperationColumns + ` FROM inserted
UNION ALL
SELECT ` + agentLifecycleOperationColumns + ` FROM agent_lifecycle_operations
JOIN claimed ON agent_lifecycle_operations.id = claimed.operation_id
WHERE claimed.operation_table = 'agent_lifecycle_operations'
LIMIT 1`

func (q *Queries) CreateAgentLifecycleOperationIdempotent(ctx context.Context, arg CreateAgentLifecycleOperationIdempotentParams) (AgentLifecycleOperation, error) {
	row := q.db.QueryRow(ctx, createAgentLifecycleOperationIdempotent,
		arg.Scope,
		arg.IdempotencyKey,
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

type FailStuckAgentUpgradeOperationsParams struct {
	StuckAfterSeconds int32  `json:"stuck_after_seconds"`
	LastError         string `json:"last_error"`
}

// failStuckAgentUpgradeOperations is the terminal backstop for an upgrade whose
// agent never came back. The success edge is a heartbeat from the replacement
// agent (MarkRunningAgentUpgradeSucceededByVersion); when that never arrives —
// bad image, watchdog dead, cluster dark — the operation would otherwise sit in
// `running` forever and a batched fleet rollout would keep marching. started_at
// is used rather than updated_at because the heartbeat re-claim path touches
// updated_at and would keep resetting the deadline.
const failStuckAgentUpgradeOperations = `-- name: FailStuckAgentUpgradeOperations :many
UPDATE agent_lifecycle_operations
SET status = 'failed',
    completed_at = COALESCE(completed_at, now()),
    last_error = $2,
    updated_at = now()
WHERE operation_type = 'agent_upgrade'
  AND status = 'running'
  AND COALESCE(started_at, created_at) < now() - ($1::int * interval '1 second')
RETURNING ` + agentLifecycleOperationColumns

func (q *Queries) FailStuckAgentUpgradeOperations(ctx context.Context, arg FailStuckAgentUpgradeOperationsParams) ([]AgentLifecycleOperation, error) {
	rows, err := q.db.Query(ctx, failStuckAgentUpgradeOperations, arg.StuckAfterSeconds, arg.LastError)
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

type MarkRunningAgentUpgradeSucceededByVersionParams struct {
	ClusterID     uuid.UUID `json:"cluster_id"`
	TargetVersion string    `json:"target_version"`
}

// markRunningAgentUpgradeSucceededByVersion is the ONLY success edge for a
// hardened agent: the patch ack no longer completes the operation, so an upgrade
// is confirmed when the REPLACEMENT agent connects and heartbeats the target
// version. That makes the comparison load-bearing, and the two sides are
// configured independently — target_version comes from the operator's request or
// config.AgentImageTag (the chart's image.agent.tag), while the heartbeat's
// AgentVersion is pkg/version.Version baked in at image build time. A bare "v"
// prefix or a case difference between them used to be invisible, because the ack
// completed the operation; now it would leave every SUCCESSFUL upgrade in
// `running` to be re-dispatched every 5 minutes and finally failed by the
// stuck-operation sweeper. So the match is normalized rather than exact.
const markRunningAgentUpgradeSucceededByVersion = `-- name: MarkRunningAgentUpgradeSucceededByVersion :execrows
UPDATE agent_lifecycle_operations
SET status = 'succeeded',
    completed_at = COALESCE(completed_at, now()),
    last_error = '',
    updated_at = now()
WHERE cluster_id = $1
  AND operation_type = 'agent_upgrade'
  AND status = 'running'
  AND ltrim(lower(btrim(target_version)), 'v') = ltrim(lower(btrim($2::text)), 'v')
  AND btrim(target_version) <> ''`

func (q *Queries) MarkRunningAgentUpgradeSucceededByVersion(ctx context.Context, arg MarkRunningAgentUpgradeSucceededByVersionParams) (int64, error) {
	result, err := q.db.Exec(ctx, markRunningAgentUpgradeSucceededByVersion, arg.ClusterID, arg.TargetVersion)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
