package handler

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// pgtype unpack rules used by the backups DTOs (matching pgx v5 native
// MarshalJSON so the wire stays identical to the pre-DTO embed shape):
//   - pgtype.Timestamptz → *string (RFC3339Nano when valid, nil when invalid)
//   - pgtype.UUID        → *string (uuid.String when valid, nil when invalid)
//
// See TestBackupResponse_WireCompat, TestBackupScheduleResponse_WireCompat
// and TestRestoreOperationResponse_WireCompat for the byte-for-byte wire
// compat assertions.

// BackupResponse is the explicit wire shape for sqlc.Backup. Enumerated so
// schema changes don't silently leak through the API.
type BackupResponse struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	StorageID          string          `json:"storage_id"`
	BackupType         string          `json:"backup_type"`
	Status             string          `json:"status"`
	FilePath           string          `json:"file_path"`
	FileSizeBytes      int64           `json:"file_size_bytes"`
	DatabaseTables     json.RawMessage `json:"database_tables"`
	StartedAt          *string         `json:"started_at"`
	CompletedAt        *string         `json:"completed_at"`
	ErrorMessage       string          `json:"error_message"`
	CreatedByID        *string         `json:"created_by_id"`
	CreatedAt          string          `json:"created_at"`
	UpdatedAt          string          `json:"updated_at"`
	ClusterID          *string         `json:"cluster_id"`
	VeleroBackupName   string          `json:"velero_backup_name"`
	VeleroNamespace    string          `json:"velero_namespace"`
	IncludedNamespaces json.RawMessage `json:"included_namespaces"`
	ExcludedNamespaces json.RawMessage `json:"excluded_namespaces"`
	PollAttempts       int32           `json:"poll_attempts"`
	LastPolledAt       *string         `json:"last_polled_at"`
}

func backupToResponse(b sqlc.Backup) BackupResponse {
	resp := BackupResponse{
		ID:                 b.ID.String(),
		Name:               b.Name,
		StorageID:          b.StorageID.String(),
		BackupType:         b.BackupType,
		Status:             b.Status,
		FilePath:           b.FilePath,
		FileSizeBytes:      b.FileSizeBytes,
		DatabaseTables:     b.DatabaseTables,
		ErrorMessage:       b.ErrorMessage,
		CreatedAt:          b.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:          b.UpdatedAt.Format(time.RFC3339Nano),
		VeleroBackupName:   b.VeleroBackupName,
		VeleroNamespace:    b.VeleroNamespace,
		IncludedNamespaces: b.IncludedNamespaces,
		ExcludedNamespaces: b.ExcludedNamespaces,
		PollAttempts:       b.PollAttempts,
	}
	if b.StartedAt.Valid {
		s := b.StartedAt.Time.Format(time.RFC3339Nano)
		resp.StartedAt = &s
	}
	if b.CompletedAt.Valid {
		s := b.CompletedAt.Time.Format(time.RFC3339Nano)
		resp.CompletedAt = &s
	}
	if b.LastPolledAt.Valid {
		s := b.LastPolledAt.Time.Format(time.RFC3339Nano)
		resp.LastPolledAt = &s
	}
	if b.CreatedByID.Valid {
		s := uuid.UUID(b.CreatedByID.Bytes).String()
		resp.CreatedByID = &s
	}
	if b.ClusterID.Valid {
		s := uuid.UUID(b.ClusterID.Bytes).String()
		resp.ClusterID = &s
	}
	return resp
}

// BackupScheduleResponse is the explicit wire shape for sqlc.BackupSchedule.
type BackupScheduleResponse struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	StorageID          string          `json:"storage_id"`
	BackupType         string          `json:"backup_type"`
	CronExpression     string          `json:"cron_expression"`
	RetentionCount     int32           `json:"retention_count"`
	Enabled            bool            `json:"enabled"`
	LastBackupID       *string         `json:"last_backup_id"`
	CreatedByID        *string         `json:"created_by_id"`
	CreatedAt          string          `json:"created_at"`
	UpdatedAt          string          `json:"updated_at"`
	ClusterID          *string         `json:"cluster_id"`
	VeleroNamespace    string          `json:"velero_namespace"`
	VeleroScheduleName string          `json:"velero_schedule_name"`
	IncludedNamespaces json.RawMessage `json:"included_namespaces"`
	ExcludedNamespaces json.RawMessage `json:"excluded_namespaces"`
	Ttl                string          `json:"ttl"`
}

func backupScheduleToResponse(s sqlc.BackupSchedule) BackupScheduleResponse {
	resp := BackupScheduleResponse{
		ID:                 s.ID.String(),
		Name:               s.Name,
		StorageID:          s.StorageID.String(),
		BackupType:         s.BackupType,
		CronExpression:     s.CronExpression,
		RetentionCount:     s.RetentionCount,
		Enabled:            s.Enabled,
		CreatedAt:          s.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:          s.UpdatedAt.Format(time.RFC3339Nano),
		VeleroNamespace:    s.VeleroNamespace,
		VeleroScheduleName: s.VeleroScheduleName,
		IncludedNamespaces: s.IncludedNamespaces,
		ExcludedNamespaces: s.ExcludedNamespaces,
		Ttl:                s.Ttl,
	}
	if s.LastBackupID.Valid {
		v := uuid.UUID(s.LastBackupID.Bytes).String()
		resp.LastBackupID = &v
	}
	if s.CreatedByID.Valid {
		v := uuid.UUID(s.CreatedByID.Bytes).String()
		resp.CreatedByID = &v
	}
	if s.ClusterID.Valid {
		v := uuid.UUID(s.ClusterID.Bytes).String()
		resp.ClusterID = &v
	}
	return resp
}

// RestoreOperationResponse is the explicit wire shape for sqlc.RestoreOperation.
type RestoreOperationResponse struct {
	ID                 string          `json:"id"`
	BackupID           string          `json:"backup_id"`
	Status             string          `json:"status"`
	StartedAt          *string         `json:"started_at"`
	CompletedAt        *string         `json:"completed_at"`
	ErrorMessage       string          `json:"error_message"`
	InitiatedByID      *string         `json:"initiated_by_id"`
	CreatedAt          string          `json:"created_at"`
	UpdatedAt          string          `json:"updated_at"`
	ClusterID          *string         `json:"cluster_id"`
	VeleroNamespace    string          `json:"velero_namespace"`
	VeleroRestoreName  string          `json:"velero_restore_name"`
	IncludedNamespaces json.RawMessage `json:"included_namespaces"`
	NamespaceMapping   json.RawMessage `json:"namespace_mapping"`
	PollAttempts       int32           `json:"poll_attempts"`
	LastPolledAt       *string         `json:"last_polled_at"`
}

func restoreOperationToResponse(r sqlc.RestoreOperation) RestoreOperationResponse {
	resp := RestoreOperationResponse{
		ID:                 r.ID.String(),
		BackupID:           r.BackupID.String(),
		Status:             r.Status,
		ErrorMessage:       r.ErrorMessage,
		CreatedAt:          r.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:          r.UpdatedAt.Format(time.RFC3339Nano),
		VeleroNamespace:    r.VeleroNamespace,
		VeleroRestoreName:  r.VeleroRestoreName,
		IncludedNamespaces: r.IncludedNamespaces,
		NamespaceMapping:   r.NamespaceMapping,
		PollAttempts:       r.PollAttempts,
	}
	if r.StartedAt.Valid {
		s := r.StartedAt.Time.Format(time.RFC3339Nano)
		resp.StartedAt = &s
	}
	if r.CompletedAt.Valid {
		s := r.CompletedAt.Time.Format(time.RFC3339Nano)
		resp.CompletedAt = &s
	}
	if r.LastPolledAt.Valid {
		s := r.LastPolledAt.Time.Format(time.RFC3339Nano)
		resp.LastPolledAt = &s
	}
	if r.InitiatedByID.Valid {
		s := uuid.UUID(r.InitiatedByID.Bytes).String()
		resp.InitiatedByID = &s
	}
	if r.ClusterID.Valid {
		s := uuid.UUID(r.ClusterID.Bytes).String()
		resp.ClusterID = &s
	}
	return resp
}
