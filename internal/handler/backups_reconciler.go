package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	backupReconcileCadence   = 15 * time.Second
	backupReconcileBatchSize = 200
)

type veleroBackupStatusDoc struct {
	Status struct {
		Phase         string `json:"phase"`
		FailureReason string `json:"failureReason"`
		Errors        int32  `json:"errors"`
		Warnings      int32  `json:"warnings"`
	} `json:"status"`
}

type veleroRestoreStatusDoc struct {
	Status struct {
		Phase         string `json:"phase"`
		FailureReason string `json:"failureReason"`
		Errors        int32  `json:"errors"`
		Warnings      int32  `json:"warnings"`
	} `json:"status"`
}

func (h *BackupHandler) StartReconciler(ctx context.Context) {
	if h == nil || h.queries == nil || h.requester == nil {
		return
	}
	go h.runReconciler(ctx)
}

func (h *BackupHandler) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(backupReconcileCadence)
	defer ticker.Stop()
	h.reconcileOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.reconcileOnce(ctx)
		}
	}
}

func (h *BackupHandler) reconcileOnce(ctx context.Context) {
	h.reconcilePendingBackups(ctx)
	h.pollRunningBackups(ctx)
	h.reconcilePendingRestores(ctx)
	h.pollRunningRestores(ctx)
	h.enforceScheduleRetention(ctx)
}

func (h *BackupHandler) reconcilePendingBackups(ctx context.Context) {
	rows, err := h.queries.ListBackups(ctx, sqlc.ListBackupsParams{Limit: backupReconcileBatchSize, Offset: 0})
	if err != nil {
		h.logReconcileError("list pending backups", err)
		return
	}
	for _, row := range rows {
		if row.Status != "pending" || strings.TrimSpace(row.VeleroBackupName) == "" {
			continue
		}
		h.reconcilePendingBackup(ctx, row)
	}
}

func (h *BackupHandler) pollRunningBackups(ctx context.Context) {
	rows, err := h.queries.ListRunningBackupsForPolling(ctx, backupReconcileBatchSize)
	if err != nil {
		h.logReconcileError("list running backups", err)
		return
	}
	for _, row := range rows {
		h.pollRunningBackup(ctx, row)
	}
}

func (h *BackupHandler) reconcilePendingRestores(ctx context.Context) {
	rows, err := h.queries.ListRestoreOperations(ctx, sqlc.ListRestoreOperationsParams{Limit: backupReconcileBatchSize, Offset: 0})
	if err != nil {
		h.logReconcileError("list pending restores", err)
		return
	}
	for _, row := range rows {
		if row.Status != "pending" || strings.TrimSpace(row.VeleroRestoreName) == "" {
			continue
		}
		h.reconcilePendingRestore(ctx, row)
	}
}

func (h *BackupHandler) pollRunningRestores(ctx context.Context) {
	rows, err := h.queries.ListRunningRestoresForPolling(ctx, backupReconcileBatchSize)
	if err != nil {
		h.logReconcileError("list running restores", err)
		return
	}
	for _, row := range rows {
		h.pollRunningRestore(ctx, row)
	}
}

func (h *BackupHandler) reconcilePendingBackup(ctx context.Context, row sqlc.Backup) {
	storage, err := h.queries.GetBackupStorageConfigByID(ctx, row.StorageID)
	if err != nil {
		h.failBackup(ctx, row.ID, fmt.Sprintf("loading storage config: %v", err))
		return
	}
	if !row.ClusterID.Valid && !storage.ClusterID.Valid {
		h.failBackup(ctx, row.ID, "backup has no target cluster")
		return
	}
	if err := h.applyVeleroBackupForRow(ctx, row, storage); err != nil {
		h.failBackup(ctx, row.ID, fmt.Sprintf("apply velero backup: %v", err))
		return
	}
	if err := h.queries.UpdateBackupStarted(ctx, row.ID); err != nil {
		h.logReconcileError("mark backup started", err, "backup_id", row.ID.String())
	}
}

func (h *BackupHandler) pollRunningBackup(ctx context.Context, row sqlc.Backup) {
	_ = h.queries.TouchBackupPolling(ctx, row.ID)
	storage, err := h.queries.GetBackupStorageConfigByID(ctx, row.StorageID)
	if err != nil {
		h.failBackup(ctx, row.ID, fmt.Sprintf("loading storage config: %v", err))
		return
	}
	clusterID, ok := effectiveClusterID(row.ClusterID, storage.ClusterID)
	if !ok {
		h.failBackup(ctx, row.ID, "backup has no target cluster")
		return
	}
	backupName := strings.TrimSpace(row.VeleroBackupName)
	if backupName == "" {
		backupName = veleroResourceName("backup", row.Name)
	}
	namespace := firstNonEmptyString(row.VeleroNamespace, storage.VeleroNamespace, defaultVeleroNamespace)
	_, getPath := veleroCRDPath(namespace, "backups", backupName)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, getPath, nil, requestHeaders(""))
	if err != nil {
		h.logReconcileError("get velero backup", err, "backup_id", row.ID.String(), "cluster_id", clusterID)
		return
	}
	if resp.StatusCode == http.StatusNotFound {
		if err := h.applyVeleroBackupForRow(ctx, row, storage); err != nil {
			h.logReconcileError("re-apply missing velero backup", err, "backup_id", row.ID.String(), "cluster_id", clusterID)
		}
		return
	}
	if err := ensureSuccess(resp); err != nil {
		h.logReconcileError("velero backup status request failed", err, "backup_id", row.ID.String(), "cluster_id", clusterID)
		return
	}
	var doc veleroBackupStatusDoc
	if err := parseJSONResponse(resp, &doc); err != nil {
		h.logReconcileError("decode velero backup status", err, "backup_id", row.ID.String())
		return
	}
	switch normalizeVeleroPhase(doc.Status.Phase) {
	case "", "new", "inprogress", "waitingforpluginoperations", "waitingforpluginoperationspartiallyfailed", "finalizing", "deleting":
		return
	case "completed":
		err := h.queries.UpdateBackupCompleted(ctx, sqlc.UpdateBackupCompletedParams{
			ID:            row.ID,
			FilePath:      fmt.Sprintf("velero://%s/backups/%s", namespace, backupName),
			FileSizeBytes: 0,
		})
		if err != nil {
			h.logReconcileError("mark backup completed", err, "backup_id", row.ID.String())
		}
	case "failed", "failedvalidation", "partiallyfailed":
		h.failBackup(ctx, row.ID, veleroFailureMessage(doc.Status.Phase, doc.Status.FailureReason, doc.Status.Errors, doc.Status.Warnings))
	default:
		h.logReconcileDebug("backup phase still non-terminal", "backup_id", row.ID.String(), "phase", doc.Status.Phase)
	}
}

func (h *BackupHandler) reconcilePendingRestore(ctx context.Context, row sqlc.RestoreOperation) {
	backup, err := h.queries.GetBackupByID(ctx, row.BackupID)
	if err != nil {
		h.failRestore(ctx, row.ID, fmt.Sprintf("loading source backup: %v", err))
		return
	}
	if backup.Status != "completed" {
		h.failRestore(ctx, row.ID, fmt.Sprintf("source backup %s is not completed (status=%s)", backup.ID, backup.Status))
		return
	}
	if err := h.applyVeleroRestoreForRow(ctx, row, backup); err != nil {
		h.failRestore(ctx, row.ID, fmt.Sprintf("apply velero restore: %v", err))
		return
	}
	if err := h.queries.UpdateRestoreOperationStarted(ctx, row.ID); err != nil {
		h.logReconcileError("mark restore started", err, "restore_id", row.ID.String())
	}
}

func (h *BackupHandler) pollRunningRestore(ctx context.Context, row sqlc.RestoreOperation) {
	_ = h.queries.TouchRestorePolling(ctx, row.ID)
	backup, err := h.queries.GetBackupByID(ctx, row.BackupID)
	if err != nil {
		h.failRestore(ctx, row.ID, fmt.Sprintf("loading source backup: %v", err))
		return
	}
	clusterID, ok := effectiveClusterID(row.ClusterID, backup.ClusterID)
	if !ok {
		h.failRestore(ctx, row.ID, "restore has no target cluster")
		return
	}
	restoreName := strings.TrimSpace(row.VeleroRestoreName)
	if restoreName == "" {
		restoreName = veleroResourceName("restore", backup.VeleroBackupName)
	}
	namespace := firstNonEmptyString(row.VeleroNamespace, backup.VeleroNamespace, defaultVeleroNamespace)
	_, getPath := veleroCRDPath(namespace, "restores", restoreName)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, getPath, nil, requestHeaders(""))
	if err != nil {
		h.logReconcileError("get velero restore", err, "restore_id", row.ID.String(), "cluster_id", clusterID)
		return
	}
	if resp.StatusCode == http.StatusNotFound {
		if err := h.applyVeleroRestoreForRow(ctx, row, backup); err != nil {
			h.logReconcileError("re-apply missing velero restore", err, "restore_id", row.ID.String(), "cluster_id", clusterID)
		}
		return
	}
	if err := ensureSuccess(resp); err != nil {
		h.logReconcileError("velero restore status request failed", err, "restore_id", row.ID.String(), "cluster_id", clusterID)
		return
	}
	var doc veleroRestoreStatusDoc
	if err := parseJSONResponse(resp, &doc); err != nil {
		h.logReconcileError("decode velero restore status", err, "restore_id", row.ID.String())
		return
	}
	switch normalizeVeleroPhase(doc.Status.Phase) {
	case "", "new", "inprogress", "waitingforpluginoperations", "waitingforpluginoperationspartiallyfailed", "finalizing":
		return
	case "completed":
		if err := h.queries.UpdateRestoreOperationCompleted(ctx, row.ID); err != nil {
			h.logReconcileError("mark restore completed", err, "restore_id", row.ID.String())
		}
	case "failed", "failedvalidation", "partiallyfailed":
		h.failRestore(ctx, row.ID, veleroFailureMessage(doc.Status.Phase, doc.Status.FailureReason, doc.Status.Errors, doc.Status.Warnings))
	default:
		h.logReconcileDebug("restore phase still non-terminal", "restore_id", row.ID.String(), "phase", doc.Status.Phase)
	}
}

func (h *BackupHandler) applyVeleroRestoreForRow(ctx context.Context, restore sqlc.RestoreOperation, backup sqlc.Backup) error {
	if h == nil || h.requester == nil {
		return nil
	}
	clusterID, ok := effectiveClusterID(restore.ClusterID, backup.ClusterID)
	if !ok {
		return fmt.Errorf("restore has no target cluster")
	}
	restoreName := strings.TrimSpace(restore.VeleroRestoreName)
	if restoreName == "" {
		restoreName = veleroResourceName("restore", backup.VeleroBackupName)
	}
	backupName := strings.TrimSpace(backup.VeleroBackupName)
	if backupName == "" {
		backupName = veleroResourceName("backup", backup.Name)
	}
	namespace := firstNonEmptyString(restore.VeleroNamespace, backup.VeleroNamespace, defaultVeleroNamespace)
	var namespaceMapping map[string]string
	if len(restore.NamespaceMapping) > 0 {
		_ = json.Unmarshal(restore.NamespaceMapping, &namespaceMapping)
	}
	restoreDoc := renderVeleroRestore(VeleroRestoreRender{
		Name:               restoreName,
		Namespace:          namespace,
		BackupName:         backupName,
		IncludedNamespaces: veleroNamespacesFromJSON(restore.IncludedNamespaces),
		NamespaceMapping:   namespaceMapping,
	})
	body, err := json.Marshal(restoreDoc)
	if err != nil {
		return err
	}
	createPath, patchPath := veleroCRDPath(namespace, "restores", restoreName)
	return applyJSONBody(ctx, h.requester, clusterID, patchPath, createPath, body)
}

func effectiveClusterID(primary, fallback pgtype.UUID) (string, bool) {
	if primary.Valid {
		return uuid.UUID(primary.Bytes).String(), true
	}
	if fallback.Valid {
		return uuid.UUID(fallback.Bytes).String(), true
	}
	return "", false
}

func normalizeVeleroPhase(phase string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(phase), " ", ""))
}

func veleroFailureMessage(phase, failureReason string, errors, warnings int32) string {
	reason := strings.TrimSpace(failureReason)
	if reason != "" {
		return reason
	}
	if errors > 0 || warnings > 0 {
		return fmt.Sprintf("velero phase %s (errors=%d warnings=%d)", phase, errors, warnings)
	}
	if strings.TrimSpace(phase) == "" {
		return "velero operation failed"
	}
	return fmt.Sprintf("velero phase %s", phase)
}

func (h *BackupHandler) failBackup(ctx context.Context, id uuid.UUID, msg string) {
	if err := h.queries.UpdateBackupFailed(ctx, sqlc.UpdateBackupFailedParams{
		ID:           id,
		ErrorMessage: msg,
	}); err != nil {
		h.logReconcileError("mark backup failed", err, "backup_id", id.String(), "reason", msg)
	}
}

func (h *BackupHandler) failRestore(ctx context.Context, id uuid.UUID, msg string) {
	if err := h.queries.UpdateRestoreOperationFailed(ctx, sqlc.UpdateRestoreOperationFailedParams{
		ID:           id,
		ErrorMessage: msg,
	}); err != nil {
		h.logReconcileError("mark restore failed", err, "restore_id", id.String(), "reason", msg)
	}
}

func (h *BackupHandler) logReconcileError(msg string, err error, args ...any) {
	if h == nil || h.log == nil {
		return
	}
	fields := append([]any{"error", err}, args...)
	h.log.Warn(msg, fields...)
}

func (h *BackupHandler) logReconcileDebug(msg string, args ...any) {
	if h == nil || h.log == nil {
		return
	}
	h.log.Debug(msg, args...)
}
