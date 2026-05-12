// Migration 052 — per-cluster Velero snapshot models, hand-authored sqlc shim.
//
// These three table-row types mirror what `sqlc generate` would emit for
// internal/db/migrations/052_velero_snapshots.up.sql. They are kept in
// this dedicated file (rather than appended to models.go) so a future
// `sqlc generate` run against the canonical queries/migrations files
// does not clobber them — sqlc's output target is models.go, and the
// CLI is happy to leave unrelated files alone.

package sqlc

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ClusterSnapshot mirrors one row in cluster_snapshots: a single Velero
// Backup CRD on a member cluster. spec is the user-supplied Velero
// BackupSpec body; phase mirrors BackupStatus.Phase as polled by the
// background worker. expires_at is set on create when the user supplies
// a TTL; the daily cleanup worker drops rows past their TTL whose phase
// has reached a terminal value.
type ClusterSnapshot struct {
	ID              uuid.UUID          `json:"id"`
	ClusterID       uuid.UUID          `json:"cluster_id"`
	VeleroName      string             `json:"velero_name"`
	VeleroNamespace string             `json:"velero_namespace"`
	Source          string             `json:"source"`
	Spec            json.RawMessage    `json:"spec"`
	Phase           string             `json:"phase"`
	StartTime       pgtype.Timestamptz `json:"start_time"`
	CompletionTime  pgtype.Timestamptz `json:"completion_time"`
	ExpiresAt       pgtype.Timestamptz `json:"expires_at"`
	WarningsCount   int32              `json:"warnings_count"`
	ErrorsCount     int32              `json:"errors_count"`
	LastPollAt      pgtype.Timestamptz `json:"last_poll_at"`
	LastPollError   string             `json:"last_poll_error"`
	CreatedBy       pgtype.UUID        `json:"created_by"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

// ClusterRestore mirrors one row in cluster_restores: a Velero Restore
// CRD. The target_cluster_id may differ from the snapshot's source
// cluster — that's the cross-cluster restore path. The handler checks
// at create-time that the target cluster has Velero installed AND has
// access to the same BackupStorageLocation as the source.
type ClusterRestore struct {
	ID              uuid.UUID          `json:"id"`
	SnapshotID      uuid.UUID          `json:"snapshot_id"`
	TargetClusterID uuid.UUID          `json:"target_cluster_id"`
	VeleroName      string             `json:"velero_name"`
	VeleroNamespace string             `json:"velero_namespace"`
	Spec            json.RawMessage    `json:"spec"`
	Phase           string             `json:"phase"`
	StartTime       pgtype.Timestamptz `json:"start_time"`
	CompletionTime  pgtype.Timestamptz `json:"completion_time"`
	WarningsCount   int32              `json:"warnings_count"`
	ErrorsCount     int32              `json:"errors_count"`
	LastPollAt      pgtype.Timestamptz `json:"last_poll_at"`
	LastPollError   string             `json:"last_poll_error"`
	CreatedBy       pgtype.UUID        `json:"created_by"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

// ClusterSnapshotSchedule is one cron-driven snapshot definition. The
// dispatcher worker runs every minute, evaluates each enabled row's
// cron expression against `last_run_at`, and enqueues a fresh
// ClusterSnapshot when the next-run time has elapsed.
type ClusterSnapshotSchedule struct {
	ID            uuid.UUID          `json:"id"`
	ClusterID     uuid.UUID          `json:"cluster_id"`
	Name          string             `json:"name"`
	CronSchedule  string             `json:"cron_schedule"`
	Spec          json.RawMessage    `json:"spec"`
	Enabled       bool               `json:"enabled"`
	LastRunAt     pgtype.Timestamptz `json:"last_run_at"`
	LastRunStatus string             `json:"last_run_status"`
	CreatedBy     pgtype.UUID        `json:"created_by"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}
