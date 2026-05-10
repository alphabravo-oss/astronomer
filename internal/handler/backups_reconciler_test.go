package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type reconcilerBackupQuerier struct {
	*fakeBackupQuerier
	backups       map[uuid.UUID]sqlc.Backup
	restores      map[uuid.UUID]sqlc.RestoreOperation
	storages      map[uuid.UUID]sqlc.BackupStorageConfig
	backupFailed  map[uuid.UUID]string
	restoreFailed map[uuid.UUID]string
}

func newReconcilerBackupQuerier() *reconcilerBackupQuerier {
	return &reconcilerBackupQuerier{
		fakeBackupQuerier: &fakeBackupQuerier{},
		backups:           map[uuid.UUID]sqlc.Backup{},
		restores:          map[uuid.UUID]sqlc.RestoreOperation{},
		storages:          map[uuid.UUID]sqlc.BackupStorageConfig{},
		backupFailed:      map[uuid.UUID]string{},
		restoreFailed:     map[uuid.UUID]string{},
	}
}

func (q *reconcilerBackupQuerier) GetBackupStorageConfigByID(ctx context.Context, id uuid.UUID) (sqlc.BackupStorageConfig, error) {
	cfg, ok := q.storages[id]
	if !ok {
		return sqlc.BackupStorageConfig{}, errors.New("not found")
	}
	return cfg, nil
}

func (q *reconcilerBackupQuerier) ListBackups(ctx context.Context, arg sqlc.ListBackupsParams) ([]sqlc.Backup, error) {
	out := make([]sqlc.Backup, 0, len(q.backups))
	for _, row := range q.backups {
		out = append(out, row)
	}
	return out, nil
}

func (q *reconcilerBackupQuerier) ListRunningBackupsForPolling(ctx context.Context, limit int32) ([]sqlc.Backup, error) {
	out := make([]sqlc.Backup, 0, len(q.backups))
	for _, row := range q.backups {
		if row.Status == "running" && strings.TrimSpace(row.VeleroBackupName) != "" {
			out = append(out, row)
		}
	}
	return out, nil
}

func (q *reconcilerBackupQuerier) GetBackupByID(ctx context.Context, id uuid.UUID) (sqlc.Backup, error) {
	row, ok := q.backups[id]
	if !ok {
		return sqlc.Backup{}, errors.New("not found")
	}
	return row, nil
}

func (q *reconcilerBackupQuerier) UpdateBackupStarted(ctx context.Context, id uuid.UUID) error {
	row := q.backups[id]
	row.Status = "running"
	q.backups[id] = row
	return nil
}

func (q *reconcilerBackupQuerier) UpdateBackupCompleted(ctx context.Context, arg sqlc.UpdateBackupCompletedParams) error {
	row := q.backups[arg.ID]
	row.Status = "completed"
	row.FilePath = arg.FilePath
	row.FileSizeBytes = arg.FileSizeBytes
	q.backups[arg.ID] = row
	return nil
}

func (q *reconcilerBackupQuerier) UpdateBackupFailed(ctx context.Context, arg sqlc.UpdateBackupFailedParams) error {
	row := q.backups[arg.ID]
	row.Status = "failed"
	row.ErrorMessage = arg.ErrorMessage
	q.backups[arg.ID] = row
	q.backupFailed[arg.ID] = arg.ErrorMessage
	return nil
}

func (q *reconcilerBackupQuerier) TouchBackupPolling(ctx context.Context, id uuid.UUID) error {
	row := q.backups[id]
	row.PollAttempts++
	q.backups[id] = row
	return nil
}

func (q *reconcilerBackupQuerier) ListRestoreOperations(ctx context.Context, arg sqlc.ListRestoreOperationsParams) ([]sqlc.RestoreOperation, error) {
	out := make([]sqlc.RestoreOperation, 0, len(q.restores))
	for _, row := range q.restores {
		out = append(out, row)
	}
	return out, nil
}

func (q *reconcilerBackupQuerier) ListRunningRestoresForPolling(ctx context.Context, limit int32) ([]sqlc.RestoreOperation, error) {
	out := make([]sqlc.RestoreOperation, 0, len(q.restores))
	for _, row := range q.restores {
		if row.Status == "running" && strings.TrimSpace(row.VeleroRestoreName) != "" {
			out = append(out, row)
		}
	}
	return out, nil
}

func (q *reconcilerBackupQuerier) GetRestoreOperationByID(ctx context.Context, id uuid.UUID) (sqlc.RestoreOperation, error) {
	row, ok := q.restores[id]
	if !ok {
		return sqlc.RestoreOperation{}, errors.New("not found")
	}
	return row, nil
}

func (q *reconcilerBackupQuerier) UpdateRestoreOperationStarted(ctx context.Context, id uuid.UUID) error {
	row := q.restores[id]
	row.Status = "running"
	q.restores[id] = row
	return nil
}

func (q *reconcilerBackupQuerier) UpdateRestoreOperationCompleted(ctx context.Context, id uuid.UUID) error {
	row := q.restores[id]
	row.Status = "completed"
	q.restores[id] = row
	return nil
}

func (q *reconcilerBackupQuerier) UpdateRestoreOperationFailed(ctx context.Context, arg sqlc.UpdateRestoreOperationFailedParams) error {
	row := q.restores[arg.ID]
	row.Status = "failed"
	row.ErrorMessage = arg.ErrorMessage
	q.restores[arg.ID] = row
	q.restoreFailed[arg.ID] = arg.ErrorMessage
	return nil
}

func (q *reconcilerBackupQuerier) TouchRestorePolling(ctx context.Context, id uuid.UUID) error {
	row := q.restores[id]
	row.PollAttempts++
	q.restores[id] = row
	return nil
}

type reconcilerK8sRequester struct {
	responses map[string]*protocol.K8sResponsePayload
	calls     []string
}

func (r *reconcilerK8sRequester) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error) {
	key := method + " " + path
	r.calls = append(r.calls, key)
	if resp, ok := r.responses[key]; ok {
		return resp, nil
	}
	return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound, Body: base64.StdEncoding.EncodeToString([]byte("not found"))}, nil
}

func encodedJSONResponse(status int, body any) *protocol.K8sResponsePayload {
	raw, _ := json.Marshal(body)
	return &protocol.K8sResponsePayload{
		StatusCode: status,
		Body:       base64.StdEncoding.EncodeToString(raw),
	}
}

func TestBackupReconcilerTransitionsPendingBackupToCompleted(t *testing.T) {
	clusterID := uuid.New()
	storageID := uuid.New()
	backupID := uuid.New()

	q := newReconcilerBackupQuerier()
	q.storages[storageID] = sqlc.BackupStorageConfig{
		ID:              storageID,
		ClusterID:       pgtype.UUID{Bytes: clusterID, Valid: true},
		VeleroNamespace: "velero",
		BslName:         "primary",
		Bucket:          "backups",
		Prefix:          "demo",
		StorageType:     "s3",
	}
	q.backups[backupID] = sqlc.Backup{
		ID:               backupID,
		Name:             "team-a",
		StorageID:        storageID,
		Status:           "pending",
		ClusterID:        pgtype.UUID{Bytes: clusterID, Valid: true},
		VeleroBackupName: "backup-team-a",
		VeleroNamespace:  "velero",
	}

	requester := &reconcilerK8sRequester{
		responses: map[string]*protocol.K8sResponsePayload{
			http.MethodPatch + " /apis/velero.io/v1/namespaces/velero/backups/backup-team-a": {StatusCode: http.StatusNotFound, Body: base64.StdEncoding.EncodeToString([]byte(`{}`))},
			http.MethodPost + " /apis/velero.io/v1/namespaces/velero/backups":                encodedJSONResponse(http.StatusCreated, map[string]any{"kind": "Backup"}),
			http.MethodGet + " /apis/velero.io/v1/namespaces/velero/backups/backup-team-a":   encodedJSONResponse(http.StatusOK, map[string]any{"status": map[string]any{"phase": "Completed"}}),
		},
	}

	h := &BackupHandler{queries: q, requester: requester}
	h.reconcileOnce(context.Background())

	row := q.backups[backupID]
	if row.Status != "completed" {
		t.Fatalf("backup status = %q, want completed", row.Status)
	}
	if row.FilePath != "velero://velero/backups/backup-team-a" {
		t.Fatalf("backup file_path = %q", row.FilePath)
	}
	if row.PollAttempts == 0 {
		t.Fatalf("expected backup poll attempts to increment")
	}
}

func TestBackupReconcilerAppliesPendingRestoreAndConvergesCompleted(t *testing.T) {
	clusterID := uuid.New()
	storageID := uuid.New()
	backupID := uuid.New()
	restoreID := uuid.New()

	q := newReconcilerBackupQuerier()
	q.storages[storageID] = sqlc.BackupStorageConfig{
		ID:              storageID,
		ClusterID:       pgtype.UUID{Bytes: clusterID, Valid: true},
		VeleroNamespace: "velero",
	}
	q.backups[backupID] = sqlc.Backup{
		ID:               backupID,
		Name:             "team-a",
		StorageID:        storageID,
		Status:           "completed",
		ClusterID:        pgtype.UUID{Bytes: clusterID, Valid: true},
		VeleroBackupName: "backup-team-a",
		VeleroNamespace:  "velero",
	}
	q.restores[restoreID] = sqlc.RestoreOperation{
		ID:                restoreID,
		BackupID:          backupID,
		Status:            "pending",
		ClusterID:         pgtype.UUID{Bytes: clusterID, Valid: true},
		VeleroRestoreName: "restore-backup-team-a",
		VeleroNamespace:   "velero",
		NamespaceMapping:  []byte(`{"team-a":"team-a-restored"}`),
	}

	requester := &reconcilerK8sRequester{
		responses: map[string]*protocol.K8sResponsePayload{
			http.MethodPatch + " /apis/velero.io/v1/namespaces/velero/restores/restore-backup-team-a": {StatusCode: http.StatusNotFound, Body: base64.StdEncoding.EncodeToString([]byte(`{}`))},
			http.MethodPost + " /apis/velero.io/v1/namespaces/velero/restores":                        encodedJSONResponse(http.StatusCreated, map[string]any{"kind": "Restore"}),
			http.MethodGet + " /apis/velero.io/v1/namespaces/velero/restores/restore-backup-team-a":   encodedJSONResponse(http.StatusOK, map[string]any{"status": map[string]any{"phase": "Completed"}}),
		},
	}

	h := &BackupHandler{queries: q, requester: requester}
	h.reconcileOnce(context.Background())

	row := q.restores[restoreID]
	if row.Status != "completed" {
		t.Fatalf("restore status = %q, want completed", row.Status)
	}
	if row.PollAttempts == 0 {
		t.Fatalf("expected restore poll attempts to increment")
	}
}

func TestBackupReconcilerFailsRestoreWhenSourceBackupIsNotCompleted(t *testing.T) {
	clusterID := uuid.New()
	storageID := uuid.New()
	backupID := uuid.New()
	restoreID := uuid.New()

	q := newReconcilerBackupQuerier()
	q.storages[storageID] = sqlc.BackupStorageConfig{
		ID:        storageID,
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true},
	}
	q.backups[backupID] = sqlc.Backup{
		ID:        backupID,
		StorageID: storageID,
		Status:    "running",
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true},
	}
	q.restores[restoreID] = sqlc.RestoreOperation{
		ID:                restoreID,
		BackupID:          backupID,
		Status:            "pending",
		ClusterID:         pgtype.UUID{Bytes: clusterID, Valid: true},
		VeleroRestoreName: "restore-backup-team-a",
	}

	h := &BackupHandler{queries: q, requester: &reconcilerK8sRequester{responses: map[string]*protocol.K8sResponsePayload{}}}
	h.reconcilePendingRestores(context.Background())

	row := q.restores[restoreID]
	if row.Status != "failed" {
		t.Fatalf("restore status = %q, want failed", row.Status)
	}
	if !strings.Contains(q.restoreFailed[restoreID], "is not completed") {
		t.Fatalf("restore failure message = %q", q.restoreFailed[restoreID])
	}
}
