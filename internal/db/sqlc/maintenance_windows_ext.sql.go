// Migration 057 — maintenance windows + deferred operations CRUD,
// hand-authored sqlc shim.
//
// Mirrors what `sqlc generate` would emit for queries/maintenance_windows.sql.
// We keep the file outside the canonical models.go / *.sql.go output
// targets so a future regeneration run does not clobber the additions.
// See cluster_snapshots_ext.sql.go for the broader pattern rationale.

package sqlc

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ----------------------------------------------------------------------
// maintenance_windows
// ----------------------------------------------------------------------

const maintenanceWindowSelectColumns = `
    id, name, description, mode, cron_open, duration_minutes, timezone,
    cluster_selector, operation_types, on_block, enabled,
    created_by, created_at, updated_at`

func scanMaintenanceWindowRow(row interface {
	Scan(dest ...any) error
}) (MaintenanceWindow, error) {
	var i MaintenanceWindow
	err := row.Scan(
		&i.ID,
		&i.Name,
		&i.Description,
		&i.Mode,
		&i.CronOpen,
		&i.DurationMinutes,
		&i.Timezone,
		&i.ClusterSelector,
		&i.OperationTypes,
		&i.OnBlock,
		&i.Enabled,
		&i.CreatedBy,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listMaintenanceWindows = `-- name: ListMaintenanceWindows :many
SELECT ` + maintenanceWindowSelectColumns + `
FROM maintenance_windows
ORDER BY name ASC`

// ListMaintenanceWindows returns every window, enabled or not. Used by
// the admin handler to render the full configuration table.
func (q *Queries) ListMaintenanceWindows(ctx context.Context) ([]MaintenanceWindow, error) {
	rows, err := q.db.Query(ctx, listMaintenanceWindows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MaintenanceWindow{}
	for rows.Next() {
		i, err := scanMaintenanceWindowRow(rows)
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

const listEnabledMaintenanceWindows = `-- name: ListEnabledMaintenanceWindows :many
SELECT ` + maintenanceWindowSelectColumns + `
FROM maintenance_windows
WHERE enabled = true
ORDER BY name ASC`

// ListEnabledMaintenanceWindows is the hot-path read for the evaluator.
// Cached for 30s in-memory; PUT/DELETE on a window invalidates the
// cache. Empty-result is the normal case (operator opt-in feature).
func (q *Queries) ListEnabledMaintenanceWindows(ctx context.Context) ([]MaintenanceWindow, error) {
	rows, err := q.db.Query(ctx, listEnabledMaintenanceWindows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MaintenanceWindow{}
	for rows.Next() {
		i, err := scanMaintenanceWindowRow(rows)
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

const getMaintenanceWindow = `-- name: GetMaintenanceWindow :one
SELECT ` + maintenanceWindowSelectColumns + `
FROM maintenance_windows
WHERE id = $1`

func (q *Queries) GetMaintenanceWindow(ctx context.Context, id uuid.UUID) (MaintenanceWindow, error) {
	return scanMaintenanceWindowRow(q.db.QueryRow(ctx, getMaintenanceWindow, id))
}

const getMaintenanceWindowByName = `-- name: GetMaintenanceWindowByName :one
SELECT ` + maintenanceWindowSelectColumns + `
FROM maintenance_windows
WHERE name = $1`

func (q *Queries) GetMaintenanceWindowByName(ctx context.Context, name string) (MaintenanceWindow, error) {
	return scanMaintenanceWindowRow(q.db.QueryRow(ctx, getMaintenanceWindowByName, name))
}

const createMaintenanceWindow = `-- name: CreateMaintenanceWindow :one
INSERT INTO maintenance_windows (
    name, description, mode, cron_open, duration_minutes, timezone,
    cluster_selector, operation_types, on_block, enabled, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING ` + maintenanceWindowSelectColumns

// CreateMaintenanceWindowParams is the bind set for CreateMaintenanceWindow.
type CreateMaintenanceWindowParams struct {
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Mode             string          `json:"mode"`
	CronOpen         string          `json:"cron_open"`
	DurationMinutes  int32           `json:"duration_minutes"`
	Timezone         string          `json:"timezone"`
	ClusterSelector  json.RawMessage `json:"cluster_selector"`
	OperationTypes   json.RawMessage `json:"operation_types"`
	OnBlock          string          `json:"on_block"`
	Enabled          bool            `json:"enabled"`
	CreatedBy        pgtype.UUID     `json:"created_by"`
}

func (q *Queries) CreateMaintenanceWindow(ctx context.Context, arg CreateMaintenanceWindowParams) (MaintenanceWindow, error) {
	row := q.db.QueryRow(ctx, createMaintenanceWindow,
		arg.Name,
		arg.Description,
		arg.Mode,
		arg.CronOpen,
		arg.DurationMinutes,
		arg.Timezone,
		arg.ClusterSelector,
		arg.OperationTypes,
		arg.OnBlock,
		arg.Enabled,
		arg.CreatedBy,
	)
	return scanMaintenanceWindowRow(row)
}

const updateMaintenanceWindow = `-- name: UpdateMaintenanceWindow :one
UPDATE maintenance_windows
SET name              = $2,
    description       = $3,
    mode              = $4,
    cron_open         = $5,
    duration_minutes  = $6,
    timezone          = $7,
    cluster_selector  = $8,
    operation_types   = $9,
    on_block          = $10,
    enabled           = $11,
    updated_at        = now()
WHERE id = $1
RETURNING ` + maintenanceWindowSelectColumns

type UpdateMaintenanceWindowParams struct {
	ID               uuid.UUID       `json:"id"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Mode             string          `json:"mode"`
	CronOpen         string          `json:"cron_open"`
	DurationMinutes  int32           `json:"duration_minutes"`
	Timezone         string          `json:"timezone"`
	ClusterSelector  json.RawMessage `json:"cluster_selector"`
	OperationTypes   json.RawMessage `json:"operation_types"`
	OnBlock          string          `json:"on_block"`
	Enabled          bool            `json:"enabled"`
}

func (q *Queries) UpdateMaintenanceWindow(ctx context.Context, arg UpdateMaintenanceWindowParams) (MaintenanceWindow, error) {
	row := q.db.QueryRow(ctx, updateMaintenanceWindow,
		arg.ID,
		arg.Name,
		arg.Description,
		arg.Mode,
		arg.CronOpen,
		arg.DurationMinutes,
		arg.Timezone,
		arg.ClusterSelector,
		arg.OperationTypes,
		arg.OnBlock,
		arg.Enabled,
	)
	return scanMaintenanceWindowRow(row)
}

const deleteMaintenanceWindow = `-- name: DeleteMaintenanceWindow :exec
DELETE FROM maintenance_windows WHERE id = $1`

func (q *Queries) DeleteMaintenanceWindow(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteMaintenanceWindow, id)
	return err
}

// ----------------------------------------------------------------------
// deferred_operations
// ----------------------------------------------------------------------

const deferredOperationSelectColumns = `
    id, window_id, operation_type, operation_spec, target_cluster_id,
    target_project_id, status, deferred_until, expires_at, requested_by,
    last_error, dispatched_at, created_at, updated_at`

func scanDeferredOperationRow(row interface {
	Scan(dest ...any) error
}) (DeferredOperation, error) {
	var i DeferredOperation
	err := row.Scan(
		&i.ID,
		&i.WindowID,
		&i.OperationType,
		&i.OperationSpec,
		&i.TargetClusterID,
		&i.TargetProjectID,
		&i.Status,
		&i.DeferredUntil,
		&i.ExpiresAt,
		&i.RequestedBy,
		&i.LastError,
		&i.DispatchedAt,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const createDeferredOperation = `-- name: CreateDeferredOperation :one
INSERT INTO deferred_operations (
    window_id, operation_type, operation_spec, target_cluster_id, target_project_id,
    deferred_until, expires_at, requested_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING ` + deferredOperationSelectColumns

type CreateDeferredOperationParams struct {
	WindowID        uuid.UUID          `json:"window_id"`
	OperationType   string             `json:"operation_type"`
	OperationSpec   json.RawMessage    `json:"operation_spec"`
	TargetClusterID pgtype.UUID        `json:"target_cluster_id"`
	TargetProjectID pgtype.UUID        `json:"target_project_id"`
	DeferredUntil   pgtype.Timestamptz `json:"deferred_until"`
	ExpiresAt       pgtype.Timestamptz `json:"expires_at"`
	RequestedBy     pgtype.UUID        `json:"requested_by"`
}

func (q *Queries) CreateDeferredOperation(ctx context.Context, arg CreateDeferredOperationParams) (DeferredOperation, error) {
	row := q.db.QueryRow(ctx, createDeferredOperation,
		arg.WindowID,
		arg.OperationType,
		arg.OperationSpec,
		arg.TargetClusterID,
		arg.TargetProjectID,
		arg.DeferredUntil,
		arg.ExpiresAt,
		arg.RequestedBy,
	)
	return scanDeferredOperationRow(row)
}

const getDeferredOperation = `-- name: GetDeferredOperation :one
SELECT ` + deferredOperationSelectColumns + `
FROM deferred_operations
WHERE id = $1`

func (q *Queries) GetDeferredOperation(ctx context.Context, id uuid.UUID) (DeferredOperation, error) {
	return scanDeferredOperationRow(q.db.QueryRow(ctx, getDeferredOperation, id))
}

const listDeferredOperations = `-- name: ListDeferredOperations :many
SELECT ` + deferredOperationSelectColumns + `
FROM deferred_operations
ORDER BY created_at DESC
LIMIT $1 OFFSET $2`

type ListDeferredOperationsParams struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

func (q *Queries) ListDeferredOperations(ctx context.Context, arg ListDeferredOperationsParams) ([]DeferredOperation, error) {
	rows, err := q.db.Query(ctx, listDeferredOperations, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []DeferredOperation{}
	for rows.Next() {
		i, err := scanDeferredOperationRow(rows)
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

const listPendingDeferredOperations = `-- name: ListPendingDeferredOperations :many
SELECT ` + deferredOperationSelectColumns + `
FROM deferred_operations
WHERE status = 'pending'
  AND (deferred_until IS NULL OR deferred_until <= $1)
ORDER BY deferred_until ASC NULLS FIRST
LIMIT $2`

type ListPendingDeferredOperationsParams struct {
	Now   pgtype.Timestamptz `json:"now"`
	Limit int32              `json:"limit"`
}

// ListPendingDeferredOperations is the dispatcher's pull. The partial
// index idx_deferred_operations_pending makes this an O(matching rows)
// scan even when the table is large.
func (q *Queries) ListPendingDeferredOperations(ctx context.Context, arg ListPendingDeferredOperationsParams) ([]DeferredOperation, error) {
	rows, err := q.db.Query(ctx, listPendingDeferredOperations, arg.Now, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []DeferredOperation{}
	for rows.Next() {
		i, err := scanDeferredOperationRow(rows)
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

const markDeferredDispatched = `-- name: MarkDeferredDispatched :exec
UPDATE deferred_operations
SET status        = 'dispatched',
    dispatched_at = $2,
    updated_at    = now()
WHERE id = $1`

type MarkDeferredDispatchedParams struct {
	ID           uuid.UUID          `json:"id"`
	DispatchedAt pgtype.Timestamptz `json:"dispatched_at"`
}

func (q *Queries) MarkDeferredDispatched(ctx context.Context, arg MarkDeferredDispatchedParams) error {
	_, err := q.db.Exec(ctx, markDeferredDispatched, arg.ID, arg.DispatchedAt)
	return err
}

const markDeferredExpired = `-- name: MarkDeferredExpired :exec
UPDATE deferred_operations
SET status     = 'expired',
    last_error = $2,
    updated_at = now()
WHERE id = $1`

type MarkDeferredExpiredParams struct {
	ID        uuid.UUID `json:"id"`
	LastError string    `json:"last_error"`
}

func (q *Queries) MarkDeferredExpired(ctx context.Context, arg MarkDeferredExpiredParams) error {
	_, err := q.db.Exec(ctx, markDeferredExpired, arg.ID, arg.LastError)
	return err
}

const markDeferredCancelled = `-- name: MarkDeferredCancelled :exec
UPDATE deferred_operations
SET status     = 'cancelled',
    last_error = $2,
    updated_at = now()
WHERE id = $1`

type MarkDeferredCancelledParams struct {
	ID        uuid.UUID `json:"id"`
	LastError string    `json:"last_error"`
}

func (q *Queries) MarkDeferredCancelled(ctx context.Context, arg MarkDeferredCancelledParams) error {
	_, err := q.db.Exec(ctx, markDeferredCancelled, arg.ID, arg.LastError)
	return err
}

const markDeferredFailed = `-- name: MarkDeferredFailed :exec
UPDATE deferred_operations
SET last_error = $2,
    updated_at = now()
WHERE id = $1`

type MarkDeferredFailedParams struct {
	ID        uuid.UUID `json:"id"`
	LastError string    `json:"last_error"`
}

func (q *Queries) MarkDeferredFailed(ctx context.Context, arg MarkDeferredFailedParams) error {
	_, err := q.db.Exec(ctx, markDeferredFailed, arg.ID, arg.LastError)
	return err
}

const countDeferredOperations = `-- name: CountDeferredOperations :one
SELECT COUNT(*) FROM deferred_operations`

func (q *Queries) CountDeferredOperations(ctx context.Context) (int64, error) {
	var count int64
	err := q.db.QueryRow(ctx, countDeferredOperations).Scan(&count)
	return count, err
}
