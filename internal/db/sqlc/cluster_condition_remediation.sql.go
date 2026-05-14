// Migration 086 — cluster_condition_remediation_attempts.
//
// Hand-authored sqlc shim. See queries/cluster_condition_remediation.sql
// for the canonical SQL. Format mirrors kubectl_sessions.sql.go so a
// future regen pass produces byte-compatible output.

package sqlc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ClusterConditionRemediationAttempt is the row shape for the
// cluster_condition_remediation_attempts table.
type ClusterConditionRemediationAttempt struct {
	ID            uuid.UUID       `json:"id"`
	ClusterID     uuid.UUID       `json:"cluster_id"`
	ConditionType string          `json:"condition_type"`
	Action        string          `json:"action"`
	Outcome       string          `json:"outcome"`
	Error         string          `json:"error"`
	Detail        json.RawMessage `json:"detail"`
	AttemptedAt   time.Time       `json:"attempted_at"`
}

// InsertClusterConditionRemediationParams mirrors the INSERT.
type InsertClusterConditionRemediationParams struct {
	ClusterID     uuid.UUID       `json:"cluster_id"`
	ConditionType string          `json:"condition_type"`
	Action        string          `json:"action"`
	Outcome       string          `json:"outcome"`
	Error         string          `json:"error"`
	Detail        json.RawMessage `json:"detail"`
}

const listClusterConditionsByStatus = `-- name: ListClusterConditionsByStatus :many
SELECT c.id, c.cluster_id, c.type, c.status, c.reason, c.message,
       c.last_transition_time, c.last_probe_time, c.created_at, c.updated_at
FROM cluster_conditions c
JOIN clusters cl ON cl.id = c.cluster_id
WHERE c.status = $1 AND cl.decommissioned_at IS NULL
ORDER BY c.last_transition_time ASC
`

func (q *Queries) ListClusterConditionsByStatus(ctx context.Context, status string) ([]ClusterCondition, error) {
	rows, err := q.db.Query(ctx, listClusterConditionsByStatus, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClusterCondition{}
	for rows.Next() {
		var i ClusterCondition
		if err := rows.Scan(
			&i.ID,
			&i.ClusterID,
			&i.Type,
			&i.Status,
			&i.Reason,
			&i.Message,
			&i.LastTransitionTime,
			&i.LastProbeTime,
			&i.CreatedAt,
			&i.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

const insertClusterConditionRemediation = `-- name: InsertClusterConditionRemediation :one
INSERT INTO cluster_condition_remediation_attempts
    (cluster_id, condition_type, action, outcome, error, detail)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, cluster_id, condition_type, action, outcome, error, detail, attempted_at
`

func (q *Queries) InsertClusterConditionRemediation(ctx context.Context, arg InsertClusterConditionRemediationParams) (ClusterConditionRemediationAttempt, error) {
	row := q.db.QueryRow(ctx, insertClusterConditionRemediation,
		arg.ClusterID,
		arg.ConditionType,
		arg.Action,
		arg.Outcome,
		arg.Error,
		arg.Detail,
	)
	var i ClusterConditionRemediationAttempt
	err := row.Scan(
		&i.ID,
		&i.ClusterID,
		&i.ConditionType,
		&i.Action,
		&i.Outcome,
		&i.Error,
		&i.Detail,
		&i.AttemptedAt,
	)
	return i, err
}

const getLatestClusterConditionRemediation = `-- name: GetLatestClusterConditionRemediation :one
SELECT id, cluster_id, condition_type, action, outcome, error, detail, attempted_at
FROM cluster_condition_remediation_attempts
WHERE cluster_id = $1 AND condition_type = $2
ORDER BY attempted_at DESC
LIMIT 1
`

// GetLatestClusterConditionRemediationParams mirrors the SELECT bind.
type GetLatestClusterConditionRemediationParams struct {
	ClusterID     uuid.UUID `json:"cluster_id"`
	ConditionType string    `json:"condition_type"`
}

func (q *Queries) GetLatestClusterConditionRemediation(ctx context.Context, arg GetLatestClusterConditionRemediationParams) (ClusterConditionRemediationAttempt, error) {
	row := q.db.QueryRow(ctx, getLatestClusterConditionRemediation, arg.ClusterID, arg.ConditionType)
	var i ClusterConditionRemediationAttempt
	err := row.Scan(
		&i.ID,
		&i.ClusterID,
		&i.ConditionType,
		&i.Action,
		&i.Outcome,
		&i.Error,
		&i.Detail,
		&i.AttemptedAt,
	)
	return i, err
}

const listClusterConditionRemediationByCluster = `-- name: ListClusterConditionRemediationByCluster :many
SELECT id, cluster_id, condition_type, action, outcome, error, detail, attempted_at
FROM cluster_condition_remediation_attempts
WHERE cluster_id = $1
ORDER BY attempted_at DESC
LIMIT 50
`

func (q *Queries) ListClusterConditionRemediationByCluster(ctx context.Context, clusterID uuid.UUID) ([]ClusterConditionRemediationAttempt, error) {
	rows, err := q.db.Query(ctx, listClusterConditionRemediationByCluster, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClusterConditionRemediationAttempt{}
	for rows.Next() {
		var i ClusterConditionRemediationAttempt
		if err := rows.Scan(
			&i.ID,
			&i.ClusterID,
			&i.ConditionType,
			&i.Action,
			&i.Outcome,
			&i.Error,
			&i.Detail,
			&i.AttemptedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

const countClusterConditionRemediationSinceForType = `-- name: CountClusterConditionRemediationSinceForType :one
SELECT count(*)
FROM cluster_condition_remediation_attempts
WHERE cluster_id = $1
  AND condition_type = $2
  AND outcome <> 'skipped'
  AND attempted_at > $3
`

// CountClusterConditionRemediationSinceForTypeParams mirrors the bind.
type CountClusterConditionRemediationSinceForTypeParams struct {
	ClusterID     uuid.UUID `json:"cluster_id"`
	ConditionType string    `json:"condition_type"`
	AttemptedAt   time.Time `json:"attempted_at"`
}

func (q *Queries) CountClusterConditionRemediationSinceForType(ctx context.Context, arg CountClusterConditionRemediationSinceForTypeParams) (int64, error) {
	row := q.db.QueryRow(ctx, countClusterConditionRemediationSinceForType, arg.ClusterID, arg.ConditionType, arg.AttemptedAt)
	var count int64
	err := row.Scan(&count)
	return count, err
}
