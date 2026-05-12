// Migration 052 — per-cluster Velero snapshot CRUD, hand-authored sqlc shim.
//
// Mirrors what `sqlc generate` would emit for queries/cluster_snapshots.sql.
// We keep the file outside the canonical models.go / clusters.sql.go output
// targets so a future regeneration run doesn't clobber the hand additions.
// Tracking issue + the broader "hand sqlc shim" pattern lives next to the
// cluster_registry_configs_ext.sql.go file from migration 050.

package sqlc

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ----------------------------------------------------------------------
// cluster_snapshots
// ----------------------------------------------------------------------

const clusterSnapshotSelectColumns = `
    id, cluster_id, velero_name, velero_namespace, source, spec, phase,
    start_time, completion_time, expires_at,
    warnings_count, errors_count, last_poll_at, last_poll_error,
    created_by, created_at, updated_at`

func scanClusterSnapshotRow(row interface {
	Scan(dest ...any) error
}) (ClusterSnapshot, error) {
	var i ClusterSnapshot
	err := row.Scan(
		&i.ID,
		&i.ClusterID,
		&i.VeleroName,
		&i.VeleroNamespace,
		&i.Source,
		&i.Spec,
		&i.Phase,
		&i.StartTime,
		&i.CompletionTime,
		&i.ExpiresAt,
		&i.WarningsCount,
		&i.ErrorsCount,
		&i.LastPollAt,
		&i.LastPollError,
		&i.CreatedBy,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listClusterSnapshots = `-- name: ListClusterSnapshots :many
SELECT ` + clusterSnapshotSelectColumns + `
FROM cluster_snapshots
WHERE cluster_id = $1
ORDER BY created_at DESC`

func (q *Queries) ListClusterSnapshots(ctx context.Context, clusterID uuid.UUID) ([]ClusterSnapshot, error) {
	rows, err := q.db.Query(ctx, listClusterSnapshots, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ClusterSnapshot{}
	for rows.Next() {
		i, err := scanClusterSnapshotRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getClusterSnapshotByID = `-- name: GetClusterSnapshotByID :one
SELECT ` + clusterSnapshotSelectColumns + `
FROM cluster_snapshots
WHERE id = $1`

func (q *Queries) GetClusterSnapshotByID(ctx context.Context, id uuid.UUID) (ClusterSnapshot, error) {
	return scanClusterSnapshotRow(q.db.QueryRow(ctx, getClusterSnapshotByID, id))
}

const createClusterSnapshot = `-- name: CreateClusterSnapshot :one
INSERT INTO cluster_snapshots (
    cluster_id, velero_name, velero_namespace, source, spec, phase, expires_at, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING ` + clusterSnapshotSelectColumns

// CreateClusterSnapshotParams is the bind set for createClusterSnapshot.
// Phase is rarely overridden by the handler (it defaults to 'New' in the
// schema) but we keep it explicit on the Params so tests can pin a
// deterministic value when needed.
type CreateClusterSnapshotParams struct {
	ClusterID       uuid.UUID          `json:"cluster_id"`
	VeleroName      string             `json:"velero_name"`
	VeleroNamespace string             `json:"velero_namespace"`
	Source          string             `json:"source"`
	Spec            json.RawMessage    `json:"spec"`
	Phase           string             `json:"phase"`
	ExpiresAt       pgtype.Timestamptz `json:"expires_at"`
	CreatedBy       pgtype.UUID        `json:"created_by"`
}

func (q *Queries) CreateClusterSnapshot(ctx context.Context, arg CreateClusterSnapshotParams) (ClusterSnapshot, error) {
	row := q.db.QueryRow(ctx, createClusterSnapshot,
		arg.ClusterID,
		arg.VeleroName,
		arg.VeleroNamespace,
		arg.Source,
		arg.Spec,
		arg.Phase,
		arg.ExpiresAt,
		arg.CreatedBy,
	)
	return scanClusterSnapshotRow(row)
}

const deleteClusterSnapshot = `-- name: DeleteClusterSnapshot :exec
DELETE FROM cluster_snapshots WHERE id = $1`

func (q *Queries) DeleteClusterSnapshot(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteClusterSnapshot, id)
	return err
}

const listPendingClusterSnapshots = `-- name: ListPendingClusterSnapshots :many
SELECT ` + clusterSnapshotSelectColumns + `
FROM cluster_snapshots
WHERE phase IN ('New','InProgress')
ORDER BY created_at ASC
LIMIT $1`

func (q *Queries) ListPendingClusterSnapshots(ctx context.Context, lim int32) ([]ClusterSnapshot, error) {
	rows, err := q.db.Query(ctx, listPendingClusterSnapshots, lim)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ClusterSnapshot{}
	for rows.Next() {
		i, err := scanClusterSnapshotRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listExpiredTerminalSnapshots = `-- name: ListExpiredTerminalSnapshots :many
SELECT ` + clusterSnapshotSelectColumns + `
FROM cluster_snapshots
WHERE expires_at IS NOT NULL
  AND expires_at < now()
  AND phase IN ('Completed','Failed','FailedValidation','PartiallyFailed','Deleted')
ORDER BY expires_at ASC
LIMIT $1`

func (q *Queries) ListExpiredTerminalSnapshots(ctx context.Context, lim int32) ([]ClusterSnapshot, error) {
	rows, err := q.db.Query(ctx, listExpiredTerminalSnapshots, lim)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ClusterSnapshot{}
	for rows.Next() {
		i, err := scanClusterSnapshotRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const markSnapshotPhase = `-- name: MarkSnapshotPhase :exec
UPDATE cluster_snapshots
SET phase           = $2,
    start_time      = $3,
    completion_time = $4,
    warnings_count  = $5,
    errors_count    = $6,
    last_poll_at    = now(),
    last_poll_error = $7,
    updated_at      = now()
WHERE id = $1`

// MarkSnapshotPhaseParams is the bind set used by the poller worker.
// Pass pgtype.Timestamptz{} for StartTime / CompletionTime to leave
// those columns NULL — they only fire once Velero advances to the
// respective state.
type MarkSnapshotPhaseParams struct {
	ID             uuid.UUID          `json:"id"`
	Phase          string             `json:"phase"`
	StartTime      pgtype.Timestamptz `json:"start_time"`
	CompletionTime pgtype.Timestamptz `json:"completion_time"`
	WarningsCount  int32              `json:"warnings_count"`
	ErrorsCount    int32              `json:"errors_count"`
	LastPollError  string             `json:"last_poll_error"`
}

func (q *Queries) MarkSnapshotPhase(ctx context.Context, arg MarkSnapshotPhaseParams) error {
	_, err := q.db.Exec(ctx, markSnapshotPhase,
		arg.ID,
		arg.Phase,
		arg.StartTime,
		arg.CompletionTime,
		arg.WarningsCount,
		arg.ErrorsCount,
		arg.LastPollError,
	)
	return err
}

// ----------------------------------------------------------------------
// cluster_restores
// ----------------------------------------------------------------------

const clusterRestoreSelectColumns = `
    id, snapshot_id, target_cluster_id, velero_name, velero_namespace,
    spec, phase, start_time, completion_time,
    warnings_count, errors_count, last_poll_at, last_poll_error,
    created_by, created_at, updated_at`

func scanClusterRestoreRow(row interface {
	Scan(dest ...any) error
}) (ClusterRestore, error) {
	var i ClusterRestore
	err := row.Scan(
		&i.ID,
		&i.SnapshotID,
		&i.TargetClusterID,
		&i.VeleroName,
		&i.VeleroNamespace,
		&i.Spec,
		&i.Phase,
		&i.StartTime,
		&i.CompletionTime,
		&i.WarningsCount,
		&i.ErrorsCount,
		&i.LastPollAt,
		&i.LastPollError,
		&i.CreatedBy,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listClusterRestores = `-- name: ListClusterRestores :many
SELECT ` + clusterRestoreSelectColumns + `
FROM cluster_restores
WHERE target_cluster_id = $1
ORDER BY created_at DESC`

func (q *Queries) ListClusterRestores(ctx context.Context, targetClusterID uuid.UUID) ([]ClusterRestore, error) {
	rows, err := q.db.Query(ctx, listClusterRestores, targetClusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ClusterRestore{}
	for rows.Next() {
		i, err := scanClusterRestoreRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getClusterRestoreByID = `-- name: GetClusterRestoreByID :one
SELECT ` + clusterRestoreSelectColumns + `
FROM cluster_restores
WHERE id = $1`

func (q *Queries) GetClusterRestoreByID(ctx context.Context, id uuid.UUID) (ClusterRestore, error) {
	return scanClusterRestoreRow(q.db.QueryRow(ctx, getClusterRestoreByID, id))
}

const createClusterRestore = `-- name: CreateClusterRestore :one
INSERT INTO cluster_restores (
    snapshot_id, target_cluster_id, velero_name, velero_namespace, spec, phase, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING ` + clusterRestoreSelectColumns

type CreateClusterRestoreParams struct {
	SnapshotID      uuid.UUID       `json:"snapshot_id"`
	TargetClusterID uuid.UUID       `json:"target_cluster_id"`
	VeleroName      string          `json:"velero_name"`
	VeleroNamespace string          `json:"velero_namespace"`
	Spec            json.RawMessage `json:"spec"`
	Phase           string          `json:"phase"`
	CreatedBy       pgtype.UUID     `json:"created_by"`
}

func (q *Queries) CreateClusterRestore(ctx context.Context, arg CreateClusterRestoreParams) (ClusterRestore, error) {
	row := q.db.QueryRow(ctx, createClusterRestore,
		arg.SnapshotID,
		arg.TargetClusterID,
		arg.VeleroName,
		arg.VeleroNamespace,
		arg.Spec,
		arg.Phase,
		arg.CreatedBy,
	)
	return scanClusterRestoreRow(row)
}

const listPendingClusterRestores = `-- name: ListPendingClusterRestores :many
SELECT ` + clusterRestoreSelectColumns + `
FROM cluster_restores
WHERE phase IN ('New','InProgress')
ORDER BY created_at ASC
LIMIT $1`

func (q *Queries) ListPendingClusterRestores(ctx context.Context, lim int32) ([]ClusterRestore, error) {
	rows, err := q.db.Query(ctx, listPendingClusterRestores, lim)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ClusterRestore{}
	for rows.Next() {
		i, err := scanClusterRestoreRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const markRestorePhase = `-- name: MarkRestorePhase :exec
UPDATE cluster_restores
SET phase           = $2,
    start_time      = $3,
    completion_time = $4,
    warnings_count  = $5,
    errors_count    = $6,
    last_poll_at    = now(),
    last_poll_error = $7,
    updated_at      = now()
WHERE id = $1`

type MarkRestorePhaseParams struct {
	ID             uuid.UUID          `json:"id"`
	Phase          string             `json:"phase"`
	StartTime      pgtype.Timestamptz `json:"start_time"`
	CompletionTime pgtype.Timestamptz `json:"completion_time"`
	WarningsCount  int32              `json:"warnings_count"`
	ErrorsCount    int32              `json:"errors_count"`
	LastPollError  string             `json:"last_poll_error"`
}

func (q *Queries) MarkRestorePhase(ctx context.Context, arg MarkRestorePhaseParams) error {
	_, err := q.db.Exec(ctx, markRestorePhase,
		arg.ID,
		arg.Phase,
		arg.StartTime,
		arg.CompletionTime,
		arg.WarningsCount,
		arg.ErrorsCount,
		arg.LastPollError,
	)
	return err
}

// ----------------------------------------------------------------------
// cluster_snapshot_schedules
// ----------------------------------------------------------------------

const clusterSnapshotScheduleSelectColumns = `
    id, cluster_id, name, cron_schedule, spec, enabled,
    last_run_at, last_run_status, created_by, created_at, updated_at`

func scanClusterSnapshotScheduleRow(row interface {
	Scan(dest ...any) error
}) (ClusterSnapshotSchedule, error) {
	var i ClusterSnapshotSchedule
	err := row.Scan(
		&i.ID,
		&i.ClusterID,
		&i.Name,
		&i.CronSchedule,
		&i.Spec,
		&i.Enabled,
		&i.LastRunAt,
		&i.LastRunStatus,
		&i.CreatedBy,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listClusterSnapshotSchedules = `-- name: ListClusterSnapshotSchedules :many
SELECT ` + clusterSnapshotScheduleSelectColumns + `
FROM cluster_snapshot_schedules
WHERE cluster_id = $1
ORDER BY name ASC`

func (q *Queries) ListClusterSnapshotSchedules(ctx context.Context, clusterID uuid.UUID) ([]ClusterSnapshotSchedule, error) {
	rows, err := q.db.Query(ctx, listClusterSnapshotSchedules, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ClusterSnapshotSchedule{}
	for rows.Next() {
		i, err := scanClusterSnapshotScheduleRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getClusterSnapshotScheduleByID = `-- name: GetClusterSnapshotScheduleByID :one
SELECT ` + clusterSnapshotScheduleSelectColumns + `
FROM cluster_snapshot_schedules
WHERE id = $1`

func (q *Queries) GetClusterSnapshotScheduleByID(ctx context.Context, id uuid.UUID) (ClusterSnapshotSchedule, error) {
	return scanClusterSnapshotScheduleRow(q.db.QueryRow(ctx, getClusterSnapshotScheduleByID, id))
}

const listEnabledSnapshotSchedules = `-- name: ListEnabledSnapshotSchedules :many
SELECT ` + clusterSnapshotScheduleSelectColumns + `
FROM cluster_snapshot_schedules
WHERE enabled = true
ORDER BY id ASC`

func (q *Queries) ListEnabledSnapshotSchedules(ctx context.Context) ([]ClusterSnapshotSchedule, error) {
	rows, err := q.db.Query(ctx, listEnabledSnapshotSchedules)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ClusterSnapshotSchedule{}
	for rows.Next() {
		i, err := scanClusterSnapshotScheduleRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const createClusterSnapshotSchedule = `-- name: CreateClusterSnapshotSchedule :one
INSERT INTO cluster_snapshot_schedules (
    cluster_id, name, cron_schedule, spec, enabled, created_by
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING ` + clusterSnapshotScheduleSelectColumns

type CreateClusterSnapshotScheduleParams struct {
	ClusterID    uuid.UUID       `json:"cluster_id"`
	Name         string          `json:"name"`
	CronSchedule string          `json:"cron_schedule"`
	Spec         json.RawMessage `json:"spec"`
	Enabled      bool            `json:"enabled"`
	CreatedBy    pgtype.UUID     `json:"created_by"`
}

func (q *Queries) CreateClusterSnapshotSchedule(ctx context.Context, arg CreateClusterSnapshotScheduleParams) (ClusterSnapshotSchedule, error) {
	row := q.db.QueryRow(ctx, createClusterSnapshotSchedule,
		arg.ClusterID,
		arg.Name,
		arg.CronSchedule,
		arg.Spec,
		arg.Enabled,
		arg.CreatedBy,
	)
	return scanClusterSnapshotScheduleRow(row)
}

const updateClusterSnapshotSchedule = `-- name: UpdateClusterSnapshotSchedule :one
UPDATE cluster_snapshot_schedules
SET name          = $2,
    cron_schedule = $3,
    spec          = $4,
    enabled       = $5,
    updated_at    = now()
WHERE id = $1
RETURNING ` + clusterSnapshotScheduleSelectColumns

type UpdateClusterSnapshotScheduleParams struct {
	ID           uuid.UUID       `json:"id"`
	Name         string          `json:"name"`
	CronSchedule string          `json:"cron_schedule"`
	Spec         json.RawMessage `json:"spec"`
	Enabled      bool            `json:"enabled"`
}

func (q *Queries) UpdateClusterSnapshotSchedule(ctx context.Context, arg UpdateClusterSnapshotScheduleParams) (ClusterSnapshotSchedule, error) {
	row := q.db.QueryRow(ctx, updateClusterSnapshotSchedule,
		arg.ID,
		arg.Name,
		arg.CronSchedule,
		arg.Spec,
		arg.Enabled,
	)
	return scanClusterSnapshotScheduleRow(row)
}

const deleteClusterSnapshotSchedule = `-- name: DeleteClusterSnapshotSchedule :exec
DELETE FROM cluster_snapshot_schedules WHERE id = $1`

func (q *Queries) DeleteClusterSnapshotSchedule(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteClusterSnapshotSchedule, id)
	return err
}

const markSnapshotScheduleRan = `-- name: MarkSnapshotScheduleRan :exec
UPDATE cluster_snapshot_schedules
SET last_run_at     = now(),
    last_run_status = $2,
    updated_at      = now()
WHERE id = $1`

type MarkSnapshotScheduleRanParams struct {
	ID            uuid.UUID `json:"id"`
	LastRunStatus string    `json:"last_run_status"`
}

func (q *Queries) MarkSnapshotScheduleRan(ctx context.Context, arg MarkSnapshotScheduleRanParams) error {
	_, err := q.db.Exec(ctx, markSnapshotScheduleRan, arg.ID, arg.LastRunStatus)
	return err
}
