package sqlc

import (
	"context"
	"encoding/json"
)

const repairJobStateSelectColumns = `
    job_name, scope, status, last_successful_reconcile_at, last_error_at,
    last_error, success_count, error_count, metadata, created_at, updated_at`

func scanRepairJobState(row interface {
	Scan(dest ...any) error
}) (RepairJobState, error) {
	var i RepairJobState
	err := row.Scan(
		&i.JobName,
		&i.Scope,
		&i.Status,
		&i.LastSuccessfulReconcileAt,
		&i.LastErrorAt,
		&i.LastError,
		&i.SuccessCount,
		&i.ErrorCount,
		&i.Metadata,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

type RecordRepairJobSuccessParams struct {
	JobName  string          `json:"job_name"`
	Scope    string          `json:"scope"`
	Metadata json.RawMessage `json:"metadata"`
}

const recordRepairJobSuccess = `-- name: RecordRepairJobSuccess :one
INSERT INTO repair_job_states (
    job_name, scope, status, last_successful_reconcile_at, last_error,
    success_count, metadata
) VALUES (
    $1, $2, 'success', now(), '', 1, COALESCE($3::jsonb, '{}'::jsonb)
)
ON CONFLICT (job_name, scope) DO UPDATE
SET status = 'success',
    last_successful_reconcile_at = now(),
    last_error = '',
    success_count = repair_job_states.success_count + 1,
    metadata = EXCLUDED.metadata,
    updated_at = now()
RETURNING ` + repairJobStateSelectColumns

func (q *Queries) RecordRepairJobSuccess(ctx context.Context, arg RecordRepairJobSuccessParams) (RepairJobState, error) {
	return scanRepairJobState(q.db.QueryRow(ctx, recordRepairJobSuccess, arg.JobName, arg.Scope, arg.Metadata))
}

type RecordRepairJobFailureParams struct {
	JobName   string          `json:"job_name"`
	Scope     string          `json:"scope"`
	LastError string          `json:"last_error"`
	Metadata  json.RawMessage `json:"metadata"`
}

const recordRepairJobFailure = `-- name: RecordRepairJobFailure :one
INSERT INTO repair_job_states (
    job_name, scope, status, last_error_at, last_error, error_count, metadata
) VALUES (
    $1, $2, 'failed', now(), $3, 1, COALESCE($4::jsonb, '{}'::jsonb)
)
ON CONFLICT (job_name, scope) DO UPDATE
SET status = 'failed',
    last_error_at = now(),
    last_error = EXCLUDED.last_error,
    error_count = repair_job_states.error_count + 1,
    metadata = EXCLUDED.metadata,
    updated_at = now()
RETURNING ` + repairJobStateSelectColumns

func (q *Queries) RecordRepairJobFailure(ctx context.Context, arg RecordRepairJobFailureParams) (RepairJobState, error) {
	return scanRepairJobState(q.db.QueryRow(ctx, recordRepairJobFailure, arg.JobName, arg.Scope, arg.LastError, arg.Metadata))
}

type GetRepairJobStateParams struct {
	JobName string `json:"job_name"`
	Scope   string `json:"scope"`
}

const getRepairJobState = `-- name: GetRepairJobState :one
SELECT ` + repairJobStateSelectColumns + `
FROM repair_job_states
WHERE job_name = $1 AND scope = $2`

func (q *Queries) GetRepairJobState(ctx context.Context, arg GetRepairJobStateParams) (RepairJobState, error) {
	return scanRepairJobState(q.db.QueryRow(ctx, getRepairJobState, arg.JobName, arg.Scope))
}
