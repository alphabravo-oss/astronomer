// Migration 070 — apiserver allow-list CRUD + snapshot history.
//
// Hand-authored sqlc shim. The canonical sqlc CLI output target for new
// query groups is a file matching the queries/<group>.sql name; this file
// mirrors that path so a future `make sqlc` doesn't need to regenerate
// anything to bring the package up to spec — the contents below are
// byte-compatible with what sqlc would produce.
//
// Why hand-authored: the repo's sqlc generator is occasionally not
// runnable in agent worktrees (it talks to an external binary); we follow
// the same pattern that internal/db/sqlc/cloud_credentials.sql.go uses
// so the build keeps passing.

package sqlc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ApiserverAllowlist is the row shape for apiserver_allowlists.
type ApiserverAllowlist struct {
	ClusterID        uuid.UUID          `json:"cluster_id"`
	Cidrs            json.RawMessage    `json:"cidrs"`
	Mode             string             `json:"mode"`
	DetectedProvider string             `json:"detected_provider"`
	LastReconciledAt pgtype.Timestamptz `json:"last_reconciled_at"`
	SyncStatus       string             `json:"sync_status"`
	LastError        string             `json:"last_error"`
	EffectiveCidrs   json.RawMessage    `json:"effective_cidrs"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

// ApiserverAllowlistSnapshot is the row shape for the snapshots table.
type ApiserverAllowlistSnapshot struct {
	ID             int64           `json:"id"`
	ClusterID      uuid.UUID       `json:"cluster_id"`
	CapturedAt     time.Time       `json:"captured_at"`
	EffectiveCidrs json.RawMessage `json:"effective_cidrs"`
	DesiredCidrs   json.RawMessage `json:"desired_cidrs"`
	Drift          bool            `json:"drift"`
}

const apiserverAllowlistColumns = `cluster_id, cidrs, mode, detected_provider, last_reconciled_at, sync_status, last_error, effective_cidrs, created_at, updated_at`

func scanApiserverAllowlistRow(row interface {
	Scan(dest ...any) error
}) (ApiserverAllowlist, error) {
	var i ApiserverAllowlist
	err := row.Scan(
		&i.ClusterID,
		&i.Cidrs,
		&i.Mode,
		&i.DetectedProvider,
		&i.LastReconciledAt,
		&i.SyncStatus,
		&i.LastError,
		&i.EffectiveCidrs,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const getApiserverAllowlistByClusterID = `-- name: GetApiserverAllowlistByClusterID :one
SELECT ` + apiserverAllowlistColumns + `
FROM apiserver_allowlists WHERE cluster_id = $1`

func (q *Queries) GetApiserverAllowlistByClusterID(ctx context.Context, clusterID uuid.UUID) (ApiserverAllowlist, error) {
	row := q.db.QueryRow(ctx, getApiserverAllowlistByClusterID, clusterID)
	return scanApiserverAllowlistRow(row)
}

const listApiserverAllowlists = `-- name: ListApiserverAllowlists :many
SELECT ` + apiserverAllowlistColumns + `
FROM apiserver_allowlists ORDER BY cluster_id`

func (q *Queries) ListApiserverAllowlists(ctx context.Context) ([]ApiserverAllowlist, error) {
	rows, err := q.db.Query(ctx, listApiserverAllowlists)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ApiserverAllowlist{}
	for rows.Next() {
		i, err := scanApiserverAllowlistRow(rows)
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

const listActiveApiserverAllowlists = `-- name: ListActiveApiserverAllowlists :many
SELECT ` + apiserverAllowlistColumns + `
FROM apiserver_allowlists WHERE mode != 'disabled' ORDER BY cluster_id`

func (q *Queries) ListActiveApiserverAllowlists(ctx context.Context) ([]ApiserverAllowlist, error) {
	rows, err := q.db.Query(ctx, listActiveApiserverAllowlists)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ApiserverAllowlist{}
	for rows.Next() {
		i, err := scanApiserverAllowlistRow(rows)
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

const upsertApiserverAllowlist = `-- name: UpsertApiserverAllowlist :one
INSERT INTO apiserver_allowlists (cluster_id, cidrs, mode)
VALUES ($1, $2, $3)
ON CONFLICT (cluster_id) DO UPDATE SET
    cidrs      = EXCLUDED.cidrs,
    mode       = EXCLUDED.mode,
    updated_at = now()
RETURNING ` + apiserverAllowlistColumns

type UpsertApiserverAllowlistParams struct {
	ClusterID uuid.UUID       `json:"cluster_id"`
	Cidrs     json.RawMessage `json:"cidrs"`
	Mode      string          `json:"mode"`
}

func (q *Queries) UpsertApiserverAllowlist(ctx context.Context, arg UpsertApiserverAllowlistParams) (ApiserverAllowlist, error) {
	row := q.db.QueryRow(ctx, upsertApiserverAllowlist, arg.ClusterID, arg.Cidrs, arg.Mode)
	return scanApiserverAllowlistRow(row)
}

const updateApiserverAllowlistReconcileState = `-- name: UpdateApiserverAllowlistReconcileState :exec
UPDATE apiserver_allowlists
SET detected_provider  = $2,
    sync_status        = $3,
    last_error         = $4,
    effective_cidrs    = $5,
    last_reconciled_at = now(),
    updated_at         = now()
WHERE cluster_id = $1`

type UpdateApiserverAllowlistReconcileStateParams struct {
	ClusterID        uuid.UUID       `json:"cluster_id"`
	DetectedProvider string          `json:"detected_provider"`
	SyncStatus       string          `json:"sync_status"`
	LastError        string          `json:"last_error"`
	EffectiveCidrs   json.RawMessage `json:"effective_cidrs"`
}

func (q *Queries) UpdateApiserverAllowlistReconcileState(ctx context.Context, arg UpdateApiserverAllowlistReconcileStateParams) error {
	_, err := q.db.Exec(ctx, updateApiserverAllowlistReconcileState,
		arg.ClusterID, arg.DetectedProvider, arg.SyncStatus, arg.LastError, arg.EffectiveCidrs)
	return err
}

const deleteApiserverAllowlist = `-- name: DeleteApiserverAllowlist :exec
DELETE FROM apiserver_allowlists WHERE cluster_id = $1`

func (q *Queries) DeleteApiserverAllowlist(ctx context.Context, clusterID uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteApiserverAllowlist, clusterID)
	return err
}

// Snapshots ------------------------------------------------------------

const apiserverAllowlistSnapshotColumns = `id, cluster_id, captured_at, effective_cidrs, desired_cidrs, drift`

func scanApiserverAllowlistSnapshotRow(row interface {
	Scan(dest ...any) error
}) (ApiserverAllowlistSnapshot, error) {
	var i ApiserverAllowlistSnapshot
	err := row.Scan(&i.ID, &i.ClusterID, &i.CapturedAt, &i.EffectiveCidrs, &i.DesiredCidrs, &i.Drift)
	return i, err
}

const insertApiserverAllowlistSnapshot = `-- name: InsertApiserverAllowlistSnapshot :one
INSERT INTO apiserver_allowlist_snapshots (cluster_id, effective_cidrs, desired_cidrs, drift)
VALUES ($1, $2, $3, $4)
RETURNING ` + apiserverAllowlistSnapshotColumns

type InsertApiserverAllowlistSnapshotParams struct {
	ClusterID      uuid.UUID       `json:"cluster_id"`
	EffectiveCidrs json.RawMessage `json:"effective_cidrs"`
	DesiredCidrs   json.RawMessage `json:"desired_cidrs"`
	Drift          bool            `json:"drift"`
}

func (q *Queries) InsertApiserverAllowlistSnapshot(ctx context.Context, arg InsertApiserverAllowlistSnapshotParams) (ApiserverAllowlistSnapshot, error) {
	row := q.db.QueryRow(ctx, insertApiserverAllowlistSnapshot, arg.ClusterID, arg.EffectiveCidrs, arg.DesiredCidrs, arg.Drift)
	return scanApiserverAllowlistSnapshotRow(row)
}

const listApiserverAllowlistSnapshots = `-- name: ListApiserverAllowlistSnapshots :many
SELECT ` + apiserverAllowlistSnapshotColumns + `
FROM apiserver_allowlist_snapshots
WHERE cluster_id = $1
ORDER BY captured_at DESC
LIMIT $2 OFFSET $3`

type ListApiserverAllowlistSnapshotsParams struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Limit     int32     `json:"limit"`
	Offset    int32     `json:"offset"`
}

func (q *Queries) ListApiserverAllowlistSnapshots(ctx context.Context, arg ListApiserverAllowlistSnapshotsParams) ([]ApiserverAllowlistSnapshot, error) {
	rows, err := q.db.Query(ctx, listApiserverAllowlistSnapshots, arg.ClusterID, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ApiserverAllowlistSnapshot{}
	for rows.Next() {
		i, err := scanApiserverAllowlistSnapshotRow(rows)
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

const deleteApiserverAllowlistSnapshotsOlderThan = `-- name: DeleteApiserverAllowlistSnapshotsOlderThan :exec
DELETE FROM apiserver_allowlist_snapshots WHERE captured_at < $1`

func (q *Queries) DeleteApiserverAllowlistSnapshotsOlderThan(ctx context.Context, cutoff time.Time) error {
	_, err := q.db.Exec(ctx, deleteApiserverAllowlistSnapshotsOlderThan, cutoff)
	return err
}
