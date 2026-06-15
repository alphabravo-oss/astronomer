package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type backupAuditQuerier struct {
	*fakeBackupQuerier

	storages  map[uuid.UUID]sqlc.BackupStorageConfig
	backups   map[uuid.UUID]sqlc.Backup
	schedules map[uuid.UUID]sqlc.BackupSchedule
	restores  map[uuid.UUID]sqlc.RestoreOperation
	audits    []sqlc.CreateAuditLogV1Params
}

func newBackupAuditQuerier() *backupAuditQuerier {
	return &backupAuditQuerier{
		fakeBackupQuerier: &fakeBackupQuerier{},
		storages:          map[uuid.UUID]sqlc.BackupStorageConfig{},
		backups:           map[uuid.UUID]sqlc.Backup{},
		schedules:         map[uuid.UUID]sqlc.BackupSchedule{},
		restores:          map[uuid.UUID]sqlc.RestoreOperation{},
	}
}

func (q *backupAuditQuerier) GetBackupStorageConfigByID(_ context.Context, id uuid.UUID) (sqlc.BackupStorageConfig, error) {
	row, ok := q.storages[id]
	if !ok {
		return sqlc.BackupStorageConfig{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *backupAuditQuerier) CreateBackupStorageConfig(_ context.Context, arg sqlc.CreateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error) {
	now := time.Now()
	row := sqlc.BackupStorageConfig{
		ID:                   uuid.New(),
		Name:                 arg.Name,
		StorageType:          arg.StorageType,
		Bucket:               arg.Bucket,
		Prefix:               arg.Prefix,
		Region:               arg.Region,
		EndpointUrl:          arg.EndpointUrl,
		AccessKey:            arg.AccessKey,
		SecretKey:            arg.SecretKey,
		IsDefault:            arg.IsDefault,
		CreatedByID:          arg.CreatedByID,
		CreatedAt:            now,
		UpdatedAt:            now,
		ClusterID:            arg.ClusterID,
		VeleroNamespace:      arg.VeleroNamespace,
		BslName:              arg.BslName,
		EncryptedCredentials: arg.EncryptedCredentials,
	}
	q.storages[row.ID] = row
	return row, nil
}

func (q *backupAuditQuerier) UpdateBackupStorageConfig(_ context.Context, arg sqlc.UpdateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error) {
	row, ok := q.storages[arg.ID]
	if !ok {
		return sqlc.BackupStorageConfig{}, pgx.ErrNoRows
	}
	row.Name = arg.Name
	row.StorageType = arg.StorageType
	row.Bucket = arg.Bucket
	row.Prefix = arg.Prefix
	row.Region = arg.Region
	row.EndpointUrl = arg.EndpointUrl
	row.AccessKey = arg.AccessKey
	row.SecretKey = arg.SecretKey
	row.IsDefault = arg.IsDefault
	row.ClusterID = arg.ClusterID
	row.VeleroNamespace = arg.VeleroNamespace
	row.BslName = arg.BslName
	row.EncryptedCredentials = arg.EncryptedCredentials
	row.UpdatedAt = time.Now()
	q.storages[arg.ID] = row
	return row, nil
}

func (q *backupAuditQuerier) DeleteBackupStorageConfig(_ context.Context, id uuid.UUID) error {
	if _, ok := q.storages[id]; !ok {
		return pgx.ErrNoRows
	}
	delete(q.storages, id)
	return nil
}

func (q *backupAuditQuerier) GetBackupByID(_ context.Context, id uuid.UUID) (sqlc.Backup, error) {
	row, ok := q.backups[id]
	if !ok {
		return sqlc.Backup{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *backupAuditQuerier) CreateBackup(_ context.Context, arg sqlc.CreateBackupParams) (sqlc.Backup, error) {
	now := time.Now()
	row := sqlc.Backup{
		ID:                 uuid.New(),
		Name:               arg.Name,
		StorageID:          arg.StorageID,
		BackupType:         arg.BackupType,
		Status:             arg.Status,
		DatabaseTables:     arg.DatabaseTables,
		CreatedByID:        arg.CreatedByID,
		CreatedAt:          now,
		UpdatedAt:          now,
		ClusterID:          arg.ClusterID,
		VeleroBackupName:   arg.VeleroBackupName,
		VeleroNamespace:    arg.VeleroNamespace,
		IncludedNamespaces: arg.IncludedNamespaces,
		ExcludedNamespaces: arg.ExcludedNamespaces,
	}
	q.backups[row.ID] = row
	return row, nil
}

func (q *backupAuditQuerier) DeleteBackup(_ context.Context, id uuid.UUID) error {
	if _, ok := q.backups[id]; !ok {
		return pgx.ErrNoRows
	}
	delete(q.backups, id)
	return nil
}

func (q *backupAuditQuerier) GetBackupScheduleByID(_ context.Context, id uuid.UUID) (sqlc.BackupSchedule, error) {
	row, ok := q.schedules[id]
	if !ok {
		return sqlc.BackupSchedule{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *backupAuditQuerier) CreateBackupSchedule(_ context.Context, arg sqlc.CreateBackupScheduleParams) (sqlc.BackupSchedule, error) {
	now := time.Now()
	row := sqlc.BackupSchedule{
		ID:                 uuid.New(),
		Name:               arg.Name,
		StorageID:          arg.StorageID,
		BackupType:         arg.BackupType,
		CronExpression:     arg.CronExpression,
		RetentionCount:     arg.RetentionCount,
		Enabled:            arg.Enabled,
		CreatedByID:        arg.CreatedByID,
		CreatedAt:          now,
		UpdatedAt:          now,
		ClusterID:          arg.ClusterID,
		VeleroNamespace:    arg.VeleroNamespace,
		VeleroScheduleName: arg.VeleroScheduleName,
		IncludedNamespaces: arg.IncludedNamespaces,
		ExcludedNamespaces: arg.ExcludedNamespaces,
		Ttl:                arg.Ttl,
	}
	q.schedules[row.ID] = row
	return row, nil
}

func (q *backupAuditQuerier) CreateRestoreOperation(_ context.Context, arg sqlc.CreateRestoreOperationParams) (sqlc.RestoreOperation, error) {
	now := time.Now()
	row := sqlc.RestoreOperation{
		ID:                 uuid.New(),
		BackupID:           arg.BackupID,
		Status:             arg.Status,
		InitiatedByID:      arg.InitiatedByID,
		CreatedAt:          now,
		UpdatedAt:          now,
		ClusterID:          arg.ClusterID,
		VeleroNamespace:    arg.VeleroNamespace,
		VeleroRestoreName:  arg.VeleroRestoreName,
		IncludedNamespaces: arg.IncludedNamespaces,
		NamespaceMapping:   arg.NamespaceMapping,
	}
	q.restores[row.ID] = row
	return row, nil
}

func (q *backupAuditQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	q.audits = append(q.audits, arg)
	return nil
}

func TestBackupMutationsAreAudited(t *testing.T) {
	q := newBackupAuditQuerier()
	h := NewBackupHandler(q)

	storageBody := map[string]any{
		"name":             "primary",
		"storage_type":     "s3",
		"bucket":           "astronomer-backups",
		"region":           "us-east-1",
		"access_key":       "AKIA_TEST",
		"secret_key":       "secret-value",
		"velero_namespace": "velero",
		"bsl_name":         "primary",
	}
	storageRec := httptest.NewRecorder()
	h.CreateStorageConfig(storageRec, backupAuditRequest(t, http.MethodPost, "/api/v1/backups/storage/", storageBody, nil))
	if storageRec.Code != http.StatusCreated {
		t.Fatalf("storage create status=%d body=%s", storageRec.Code, storageRec.Body.String())
	}
	storage := onlyBackupStorage(t, q)
	assertBackupAudit(t, q.audits[0], "backup.storage.create", "backup_storage_config", storage.ID.String(), "primary")
	assertAuditDetail(t, q.audits[0].Detail, "storage_type", "s3")
	assertAuditDetailOmit(t, q.audits[0].Detail, "secret_key")
	assertAuditDetailOmit(t, q.audits[0].Detail, "access_key")

	updateBody := storageBody
	updateBody["region"] = "us-west-2"
	updateRec := httptest.NewRecorder()
	h.UpdateStorageConfig(updateRec, backupAuditRequest(t, http.MethodPut, "/api/v1/backups/storage/"+storage.ID.String()+"/", updateBody, map[string]string{"id": storage.ID.String()}))
	if updateRec.Code != http.StatusOK {
		t.Fatalf("storage update status=%d body=%s", updateRec.Code, updateRec.Body.String())
	}
	assertBackupAudit(t, q.audits[1], "backup.storage.update", "backup_storage_config", storage.ID.String(), "primary")
	assertAuditDetail(t, q.audits[1].Detail, "region", "us-west-2")
	assertAuditDetailOmit(t, q.audits[1].Detail, "secret_key")
	assertAuditDetailOmit(t, q.audits[1].Detail, "access_key")

	backupRec := httptest.NewRecorder()
	h.CreateBackup(backupRec, backupAuditRequest(t, http.MethodPost, "/api/v1/backups/", map[string]any{
		"name":                  "manual",
		"storage_id":            storage.ID.String(),
		"backup_type":           "full",
		"included_namespaces":   []string{"argocd"},
		"excluded_namespaces":   []string{"kube-system"},
		"database_tables":       []string{},
		"additional_unused_key": "ignored",
	}, nil))
	if backupRec.Code != http.StatusCreated {
		t.Fatalf("backup create status=%d body=%s", backupRec.Code, backupRec.Body.String())
	}
	backup := onlyBackup(t, q)
	assertBackupAudit(t, q.audits[2], "backup.create", "backup", backup.ID.String(), "manual")
	assertAuditDetail(t, q.audits[2].Detail, "backup_type", "full")

	restoreRec := httptest.NewRecorder()
	h.CreateRestore(restoreRec, backupAuditRequest(t, http.MethodPost, "/api/v1/backups/"+backup.ID.String()+"/restore/", map[string]any{
		"included_namespaces": []string{"argocd"},
	}, map[string]string{"id": backup.ID.String()}))
	if restoreRec.Code != http.StatusCreated {
		t.Fatalf("restore create status=%d body=%s", restoreRec.Code, restoreRec.Body.String())
	}
	restore := onlyRestore(t, q)
	assertBackupAudit(t, q.audits[3], "backup.restore.create", "restore_operation", restore.ID.String(), "manual")
	assertAuditDetail(t, q.audits[3].Detail, "backup_id", backup.ID.String())

	deleteBackupRec := httptest.NewRecorder()
	h.DeleteBackup(deleteBackupRec, backupAuditRequest(t, http.MethodDelete, "/api/v1/backups/"+backup.ID.String()+"/", nil, map[string]string{"id": backup.ID.String()}))
	if deleteBackupRec.Code != http.StatusNoContent {
		t.Fatalf("backup delete status=%d body=%s", deleteBackupRec.Code, deleteBackupRec.Body.String())
	}
	assertBackupAudit(t, q.audits[4], "backup.delete", "backup", backup.ID.String(), "manual")

	scheduleRec := httptest.NewRecorder()
	h.CreateSchedule(scheduleRec, backupAuditRequest(t, http.MethodPost, "/api/v1/backups/schedules/", map[string]any{
		"name":                "nightly",
		"storage_id":          storage.ID.String(),
		"backup_type":         "full",
		"cron_expression":     "0 2 * * *",
		"retention_count":     7,
		"enabled":             true,
		"velero_namespace":    "velero",
		"included_namespaces": []string{"argocd"},
		"ttl":                 "168h",
	}, nil))
	if scheduleRec.Code != http.StatusCreated {
		t.Fatalf("schedule create status=%d body=%s", scheduleRec.Code, scheduleRec.Body.String())
	}
	schedule := onlySchedule(t, q)
	assertBackupAudit(t, q.audits[5], "backup.schedule.create", "backup_schedule", schedule.ID.String(), "nightly")
	assertAuditDetail(t, q.audits[5].Detail, "cron_expression", "0 2 * * *")

	triggerRec := httptest.NewRecorder()
	h.TriggerSchedule(triggerRec, backupAuditRequest(t, http.MethodPost, "/api/v1/backups/schedules/"+schedule.ID.String()+"/trigger-now/", nil, map[string]string{"id": schedule.ID.String()}))
	if triggerRec.Code != http.StatusCreated {
		t.Fatalf("schedule trigger status=%d body=%s", triggerRec.Code, triggerRec.Body.String())
	}
	assertBackupAudit(t, q.audits[6], "backup.schedule.trigger", "backup_schedule", schedule.ID.String(), "nightly")

	deleteStorageRec := httptest.NewRecorder()
	h.DeleteStorageConfig(deleteStorageRec, backupAuditRequest(t, http.MethodDelete, "/api/v1/backups/storage/"+storage.ID.String()+"/", nil, map[string]string{"id": storage.ID.String()}))
	if deleteStorageRec.Code != http.StatusNoContent {
		t.Fatalf("storage delete status=%d body=%s", deleteStorageRec.Code, deleteStorageRec.Body.String())
	}
	assertBackupAudit(t, q.audits[7], "backup.storage.delete", "backup_storage_config", storage.ID.String(), "primary")
}

func backupAuditRequest(t *testing.T, method, path string, body any, params map[string]string) *http.Request {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func onlyBackupStorage(t *testing.T, q *backupAuditQuerier) sqlc.BackupStorageConfig {
	t.Helper()
	if len(q.storages) != 1 {
		t.Fatalf("storages=%d, want 1", len(q.storages))
	}
	for _, row := range q.storages {
		return row
	}
	return sqlc.BackupStorageConfig{}
}

func onlyBackup(t *testing.T, q *backupAuditQuerier) sqlc.Backup {
	t.Helper()
	if len(q.backups) != 1 {
		t.Fatalf("backups=%d, want 1", len(q.backups))
	}
	for _, row := range q.backups {
		return row
	}
	return sqlc.Backup{}
}

func onlySchedule(t *testing.T, q *backupAuditQuerier) sqlc.BackupSchedule {
	t.Helper()
	if len(q.schedules) != 1 {
		t.Fatalf("schedules=%d, want 1", len(q.schedules))
	}
	for _, row := range q.schedules {
		return row
	}
	return sqlc.BackupSchedule{}
}

func onlyRestore(t *testing.T, q *backupAuditQuerier) sqlc.RestoreOperation {
	t.Helper()
	if len(q.restores) != 1 {
		t.Fatalf("restores=%d, want 1", len(q.restores))
	}
	for _, row := range q.restores {
		return row
	}
	return sqlc.RestoreOperation{}
}

func assertBackupAudit(t *testing.T, row sqlc.CreateAuditLogV1Params, action, resourceType, resourceID, resourceName string) {
	t.Helper()
	if row.Action != action {
		t.Fatalf("audit action=%q want %q; row=%+v", row.Action, action, row)
	}
	if row.ResourceType != resourceType {
		t.Fatalf("audit resource_type=%q want %q; row=%+v", row.ResourceType, resourceType, row)
	}
	if row.ResourceID != resourceID || row.ResourceName != resourceName {
		t.Fatalf("audit target=(%q,%q), want (%q,%q)", row.ResourceID, row.ResourceName, resourceID, resourceName)
	}
}
