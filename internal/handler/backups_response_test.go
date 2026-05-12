package handler

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// TestBackupResponse_WireCompat asserts backupToResponse produces byte-for-byte
// identical JSON to a raw json.Marshal(sqlc.Backup{...}). Two cases cover the
// pgtype Valid=true and Valid=false branches so a future schema change that
// flips a column's nullable status can't sneak past unnoticed.
func TestBackupResponse_WireCompat(t *testing.T) {
	backupID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	storageID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	creator := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	clusterID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	started := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	completed := time.Date(2026, 5, 1, 10, 15, 30, 123456789, time.UTC)
	lastPolled := time.Date(2026, 5, 1, 10, 16, 0, 0, time.UTC)
	createdAt := time.Date(2026, 4, 30, 18, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 5, 1, 10, 16, 5, 0, time.UTC)

	cases := []struct {
		name string
		row  sqlc.Backup
	}{
		{
			name: "all fields populated",
			row: sqlc.Backup{
				ID:                 backupID,
				Name:               "nightly-prod",
				StorageID:          storageID,
				BackupType:         "full",
				Status:             "succeeded",
				FilePath:           "s3://bucket/prefix/backup.tgz",
				FileSizeBytes:      12345678,
				DatabaseTables:     json.RawMessage(`["users","clusters"]`),
				StartedAt:          pgtype.Timestamptz{Time: started, Valid: true},
				CompletedAt:        pgtype.Timestamptz{Time: completed, Valid: true},
				ErrorMessage:       "",
				CreatedByID:        pgtype.UUID{Bytes: creator, Valid: true},
				CreatedAt:          createdAt,
				UpdatedAt:          updatedAt,
				ClusterID:          pgtype.UUID{Bytes: clusterID, Valid: true},
				VeleroBackupName:   "backup-abc-20260501",
				VeleroNamespace:    "velero",
				IncludedNamespaces: json.RawMessage(`["default"]`),
				ExcludedNamespaces: json.RawMessage(`["kube-system"]`),
				PollAttempts:       3,
				LastPolledAt:       pgtype.Timestamptz{Time: lastPolled, Valid: true},
			},
		},
		{
			name: "pgtype invalids",
			row: sqlc.Backup{
				ID:                 backupID,
				Name:               "n",
				StorageID:          storageID,
				BackupType:         "incremental",
				Status:             "pending",
				DatabaseTables:     json.RawMessage(`[]`),
				IncludedNamespaces: json.RawMessage(`[]`),
				ExcludedNamespaces: json.RawMessage(`[]`),
				CreatedAt:          createdAt,
				UpdatedAt:          updatedAt,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy, err := json.Marshal(tc.row)
			if err != nil {
				t.Fatalf("marshal legacy: %v", err)
			}
			dto, err := json.Marshal(backupToResponse(tc.row))
			if err != nil {
				t.Fatalf("marshal dto: %v", err)
			}
			if !bytes.Equal(legacy, dto) {
				t.Fatalf("wire mismatch:\n legacy: %s\n dto:    %s", legacy, dto)
			}
		})
	}
}

func TestBackupScheduleResponse_WireCompat(t *testing.T) {
	schedID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	storageID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	creator := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	clusterID := uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd")
	lastBackup := uuid.MustParse("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")
	createdAt := time.Date(2026, 4, 1, 6, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 4, 15, 6, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		row  sqlc.BackupSchedule
	}{
		{
			name: "all fields populated",
			row: sqlc.BackupSchedule{
				ID:                 schedID,
				Name:               "nightly",
				StorageID:          storageID,
				BackupType:         "full",
				CronExpression:     "0 2 * * *",
				RetentionCount:     7,
				Enabled:            true,
				LastBackupID:       pgtype.UUID{Bytes: lastBackup, Valid: true},
				CreatedByID:        pgtype.UUID{Bytes: creator, Valid: true},
				CreatedAt:          createdAt,
				UpdatedAt:          updatedAt,
				ClusterID:          pgtype.UUID{Bytes: clusterID, Valid: true},
				VeleroNamespace:    "velero",
				VeleroScheduleName: "schedule-nightly",
				IncludedNamespaces: json.RawMessage(`["default"]`),
				ExcludedNamespaces: json.RawMessage(`[]`),
				Ttl:                "240h",
			},
		},
		{
			name: "pgtype invalids",
			row: sqlc.BackupSchedule{
				ID:                 schedID,
				Name:               "ad-hoc",
				StorageID:          storageID,
				BackupType:         "incremental",
				CronExpression:     "@hourly",
				RetentionCount:     5,
				Enabled:            false,
				CreatedAt:          createdAt,
				UpdatedAt:          updatedAt,
				IncludedNamespaces: json.RawMessage(`[]`),
				ExcludedNamespaces: json.RawMessage(`[]`),
				Ttl:                "",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy, err := json.Marshal(tc.row)
			if err != nil {
				t.Fatalf("marshal legacy: %v", err)
			}
			dto, err := json.Marshal(backupScheduleToResponse(tc.row))
			if err != nil {
				t.Fatalf("marshal dto: %v", err)
			}
			if !bytes.Equal(legacy, dto) {
				t.Fatalf("wire mismatch:\n legacy: %s\n dto:    %s", legacy, dto)
			}
		})
	}
}

func TestRestoreOperationResponse_WireCompat(t *testing.T) {
	id := uuid.MustParse("ff000000-0000-0000-0000-000000000001")
	backupID := uuid.MustParse("ff000000-0000-0000-0000-000000000002")
	initiator := uuid.MustParse("ff000000-0000-0000-0000-000000000003")
	clusterID := uuid.MustParse("ff000000-0000-0000-0000-000000000004")
	started := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)
	completed := time.Date(2026, 5, 2, 9, 30, 0, 0, time.UTC)
	lastPolled := time.Date(2026, 5, 2, 9, 31, 0, 0, time.UTC)
	createdAt := time.Date(2026, 5, 2, 8, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 5, 2, 9, 31, 5, 0, time.UTC)

	cases := []struct {
		name string
		row  sqlc.RestoreOperation
	}{
		{
			name: "all fields populated",
			row: sqlc.RestoreOperation{
				ID:                 id,
				BackupID:           backupID,
				Status:             "succeeded",
				StartedAt:          pgtype.Timestamptz{Time: started, Valid: true},
				CompletedAt:        pgtype.Timestamptz{Time: completed, Valid: true},
				ErrorMessage:       "",
				InitiatedByID:      pgtype.UUID{Bytes: initiator, Valid: true},
				CreatedAt:          createdAt,
				UpdatedAt:          updatedAt,
				ClusterID:          pgtype.UUID{Bytes: clusterID, Valid: true},
				VeleroNamespace:    "velero",
				VeleroRestoreName:  "restore-abc",
				IncludedNamespaces: json.RawMessage(`["default"]`),
				NamespaceMapping:   json.RawMessage(`{"default":"staging"}`),
				PollAttempts:       2,
				LastPolledAt:       pgtype.Timestamptz{Time: lastPolled, Valid: true},
			},
		},
		{
			name: "pgtype invalids",
			row: sqlc.RestoreOperation{
				ID:                 id,
				BackupID:           backupID,
				Status:             "pending",
				IncludedNamespaces: json.RawMessage(`[]`),
				NamespaceMapping:   json.RawMessage(`{}`),
				CreatedAt:          createdAt,
				UpdatedAt:          updatedAt,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy, err := json.Marshal(tc.row)
			if err != nil {
				t.Fatalf("marshal legacy: %v", err)
			}
			dto, err := json.Marshal(restoreOperationToResponse(tc.row))
			if err != nil {
				t.Fatalf("marshal dto: %v", err)
			}
			if !bytes.Equal(legacy, dto) {
				t.Fatalf("wire mismatch:\n legacy: %s\n dto:    %s", legacy, dto)
			}
		})
	}
}
