// Hand-written sqlc-style shim for the cluster-registration wizard
// (migration 078). sqlc CLI is broken in this tree (see CLAUDE.md) so
// the convention is hand-rolled query functions that match the
// generated style: package-level const for the SQL, typed Params
// struct, method on *Queries that scans into a typed return.
//
// Three concern groups in this file:
//
//  1. Reading / writing the registration_phase + install_baseline
//     columns on the clusters row.  Kept narrow on purpose: we
//     deliberately AVOID adding these columns to the generated
//     Cluster struct because that would force a scan-order change on
//     every existing query against clusters.* — ~8 sites. Returning a
//     dedicated ClusterRegistrationRecord struct sidesteps the issue.
//
//  2. Cluster_registration_steps CRUD.
//
//  3. A handful of utility queries the SSE/status handlers need.
package sqlc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ClusterRegistrationRecord is the projection of clusters.* used by the
// wizard endpoints. Lives outside the generated Cluster struct so the
// migration is additive — see file comment.
type ClusterRegistrationRecord struct {
	ClusterID               uuid.UUID          `json:"cluster_id"`
	RegistrationPhase       string             `json:"registration_phase"`
	RegistrationStartedAt   pgtype.Timestamptz `json:"registration_started_at"`
	RegistrationCompletedAt pgtype.Timestamptz `json:"registration_completed_at"`
	InstallBaseline         pgtype.Bool        `json:"install_baseline"`
}

const getClusterRegistrationRecord = `-- name: GetClusterRegistrationRecord :one
SELECT id, registration_phase, registration_started_at, registration_completed_at, install_baseline
FROM clusters
WHERE id = $1
`

// GetClusterRegistrationRecord returns the wizard-relevant subset of a
// clusters row. Used by the status endpoint, the agent-connect hook,
// and every handler that needs to gate on the current phase.
func (q *Queries) GetClusterRegistrationRecord(ctx context.Context, id uuid.UUID) (ClusterRegistrationRecord, error) {
	row := q.db.QueryRow(ctx, getClusterRegistrationRecord, id)
	var r ClusterRegistrationRecord
	err := row.Scan(
		&r.ClusterID,
		&r.RegistrationPhase,
		&r.RegistrationStartedAt,
		&r.RegistrationCompletedAt,
		&r.InstallBaseline,
	)
	return r, err
}

const updateClusterRegistrationPhase = `-- name: UpdateClusterRegistrationPhase :one
UPDATE clusters
SET registration_phase = $2,
    registration_started_at = COALESCE(registration_started_at, $3),
    registration_completed_at = $4
WHERE id = $1
RETURNING id, registration_phase, registration_started_at, registration_completed_at, install_baseline
`

// UpdateClusterRegistrationPhaseParams: started_at is COALESCEd so the
// first transition stamps it and later transitions don't clobber it.
// completed_at is the caller's responsibility — pass an invalid pgtype
// for in-flight phases.
type UpdateClusterRegistrationPhaseParams struct {
	ID          uuid.UUID          `json:"id"`
	Phase       string             `json:"phase"`
	StartedAt   pgtype.Timestamptz `json:"started_at"`
	CompletedAt pgtype.Timestamptz `json:"completed_at"`
}

func (q *Queries) UpdateClusterRegistrationPhase(ctx context.Context, arg UpdateClusterRegistrationPhaseParams) (ClusterRegistrationRecord, error) {
	row := q.db.QueryRow(ctx, updateClusterRegistrationPhase, arg.ID, arg.Phase, arg.StartedAt, arg.CompletedAt)
	var r ClusterRegistrationRecord
	err := row.Scan(
		&r.ClusterID,
		&r.RegistrationPhase,
		&r.RegistrationStartedAt,
		&r.RegistrationCompletedAt,
		&r.InstallBaseline,
	)
	return r, err
}

const setClusterInstallBaseline = `-- name: SetClusterInstallBaseline :one
UPDATE clusters
SET install_baseline = $2
WHERE id = $1
RETURNING id, registration_phase, registration_started_at, registration_completed_at, install_baseline
`

type SetClusterInstallBaselineParams struct {
	ID              uuid.UUID   `json:"id"`
	InstallBaseline pgtype.Bool `json:"install_baseline"`
}

func (q *Queries) SetClusterInstallBaseline(ctx context.Context, arg SetClusterInstallBaselineParams) (ClusterRegistrationRecord, error) {
	row := q.db.QueryRow(ctx, setClusterInstallBaseline, arg.ID, arg.InstallBaseline)
	var r ClusterRegistrationRecord
	err := row.Scan(
		&r.ClusterID,
		&r.RegistrationPhase,
		&r.RegistrationStartedAt,
		&r.RegistrationCompletedAt,
		&r.InstallBaseline,
	)
	return r, err
}

// ────────────────────────────────────────────────────────────────────
// cluster_registration_steps
// ────────────────────────────────────────────────────────────────────

// ClusterRegistrationStep mirrors one row in the cluster_registration_steps
// table. Used by the wizard timeline + Provisioning tab.
type ClusterRegistrationStep struct {
	ID            uuid.UUID          `json:"id"`
	ClusterID     uuid.UUID          `json:"cluster_id"`
	StepName      string             `json:"step_name"`
	Label         string             `json:"label"`
	Status        string             `json:"status"`
	ProgressPct   int32              `json:"progress_pct"`
	DetailJSON    json.RawMessage    `json:"detail_json"`
	StartedAt     pgtype.Timestamptz `json:"started_at"`
	CompletedAt   pgtype.Timestamptz `json:"completed_at"`
	ErrorMessage  string             `json:"error_message"`
	CreatedAt     time.Time          `json:"created_at"`
	StepOrder     int32              `json:"step_order"`
}

const insertClusterRegistrationStep = `-- name: InsertClusterRegistrationStep :one
INSERT INTO cluster_registration_steps
    (cluster_id, step_name, label, status, progress_pct, detail_json, started_at, completed_at, error_message, step_order)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id, cluster_id, step_name, label, status, progress_pct, detail_json, started_at, completed_at, error_message, created_at, step_order
`

type InsertClusterRegistrationStepParams struct {
	ClusterID    uuid.UUID          `json:"cluster_id"`
	StepName     string             `json:"step_name"`
	Label        string             `json:"label"`
	Status       string             `json:"status"`
	ProgressPct  int32              `json:"progress_pct"`
	DetailJSON   json.RawMessage    `json:"detail_json"`
	StartedAt    pgtype.Timestamptz `json:"started_at"`
	CompletedAt  pgtype.Timestamptz `json:"completed_at"`
	ErrorMessage string             `json:"error_message"`
	StepOrder    int32              `json:"step_order"`
}

func (q *Queries) InsertClusterRegistrationStep(ctx context.Context, arg InsertClusterRegistrationStepParams) (ClusterRegistrationStep, error) {
	if len(arg.DetailJSON) == 0 {
		arg.DetailJSON = json.RawMessage(`{}`)
	}
	row := q.db.QueryRow(ctx, insertClusterRegistrationStep,
		arg.ClusterID,
		arg.StepName,
		arg.Label,
		arg.Status,
		arg.ProgressPct,
		arg.DetailJSON,
		arg.StartedAt,
		arg.CompletedAt,
		arg.ErrorMessage,
		arg.StepOrder,
	)
	var s ClusterRegistrationStep
	err := row.Scan(
		&s.ID,
		&s.ClusterID,
		&s.StepName,
		&s.Label,
		&s.Status,
		&s.ProgressPct,
		&s.DetailJSON,
		&s.StartedAt,
		&s.CompletedAt,
		&s.ErrorMessage,
		&s.CreatedAt,
		&s.StepOrder,
	)
	return s, err
}

const updateClusterRegistrationStep = `-- name: UpdateClusterRegistrationStep :one
UPDATE cluster_registration_steps
SET status = $2,
    progress_pct = $3,
    detail_json = COALESCE($4, detail_json),
    started_at = COALESCE(started_at, $5),
    completed_at = $6,
    error_message = $7
WHERE id = $1
RETURNING id, cluster_id, step_name, label, status, progress_pct, detail_json, started_at, completed_at, error_message, created_at, step_order
`

type UpdateClusterRegistrationStepParams struct {
	ID           uuid.UUID          `json:"id"`
	Status       string             `json:"status"`
	ProgressPct  int32              `json:"progress_pct"`
	DetailJSON   json.RawMessage    `json:"detail_json"`
	StartedAt    pgtype.Timestamptz `json:"started_at"`
	CompletedAt  pgtype.Timestamptz `json:"completed_at"`
	ErrorMessage string             `json:"error_message"`
}

func (q *Queries) UpdateClusterRegistrationStep(ctx context.Context, arg UpdateClusterRegistrationStepParams) (ClusterRegistrationStep, error) {
	row := q.db.QueryRow(ctx, updateClusterRegistrationStep,
		arg.ID,
		arg.Status,
		arg.ProgressPct,
		arg.DetailJSON,
		arg.StartedAt,
		arg.CompletedAt,
		arg.ErrorMessage,
	)
	var s ClusterRegistrationStep
	err := row.Scan(
		&s.ID,
		&s.ClusterID,
		&s.StepName,
		&s.Label,
		&s.Status,
		&s.ProgressPct,
		&s.DetailJSON,
		&s.StartedAt,
		&s.CompletedAt,
		&s.ErrorMessage,
		&s.CreatedAt,
		&s.StepOrder,
	)
	return s, err
}

const listClusterRegistrationSteps = `-- name: ListClusterRegistrationSteps :many
SELECT id, cluster_id, step_name, label, status, progress_pct, detail_json, started_at, completed_at, error_message, created_at, step_order
FROM cluster_registration_steps
WHERE cluster_id = $1
ORDER BY step_order ASC, created_at ASC
`

func (q *Queries) ListClusterRegistrationSteps(ctx context.Context, clusterID uuid.UUID) ([]ClusterRegistrationStep, error) {
	rows, err := q.db.Query(ctx, listClusterRegistrationSteps, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ClusterRegistrationStep{}
	for rows.Next() {
		var s ClusterRegistrationStep
		if err := rows.Scan(
			&s.ID,
			&s.ClusterID,
			&s.StepName,
			&s.Label,
			&s.Status,
			&s.ProgressPct,
			&s.DetailJSON,
			&s.StartedAt,
			&s.CompletedAt,
			&s.ErrorMessage,
			&s.CreatedAt,
			&s.StepOrder,
		); err != nil {
			return nil, err
		}
		items = append(items, s)
	}
	return items, rows.Err()
}

const getClusterRegistrationStep = `-- name: GetClusterRegistrationStep :one
SELECT id, cluster_id, step_name, label, status, progress_pct, detail_json, started_at, completed_at, error_message, created_at, step_order
FROM cluster_registration_steps
WHERE id = $1
`

func (q *Queries) GetClusterRegistrationStep(ctx context.Context, id uuid.UUID) (ClusterRegistrationStep, error) {
	row := q.db.QueryRow(ctx, getClusterRegistrationStep, id)
	var s ClusterRegistrationStep
	err := row.Scan(
		&s.ID,
		&s.ClusterID,
		&s.StepName,
		&s.Label,
		&s.Status,
		&s.ProgressPct,
		&s.DetailJSON,
		&s.StartedAt,
		&s.CompletedAt,
		&s.ErrorMessage,
		&s.CreatedAt,
		&s.StepOrder,
	)
	return s, err
}

const maxStepOrderForCluster = `-- name: MaxStepOrderForCluster :one
SELECT COALESCE(MAX(step_order), 0)::int
FROM cluster_registration_steps
WHERE cluster_id = $1
`

// MaxStepOrderForCluster returns the highest step_order written for the
// cluster, or 0 when no steps exist yet. Callers add 1 to determine the
// next step's order; the column is kept stable so the UI timeline stays
// in append order.
func (q *Queries) MaxStepOrderForCluster(ctx context.Context, clusterID uuid.UUID) (int32, error) {
	row := q.db.QueryRow(ctx, maxStepOrderForCluster, clusterID)
	var n int32
	err := row.Scan(&n)
	if err == pgx.ErrNoRows {
		return 0, nil
	}
	return n, err
}
