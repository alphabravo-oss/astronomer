package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// ListSchedules handles GET /api/v1/backups/schedules/.
func (h *BackupHandler) ListSchedules(w http.ResponseWriter, r *http.Request) {
	allow, restricted, ok := h.authz.resourceScopeFilter(w, r, rbac.ResourceBackups, rbac.VerbRead)
	if !ok {
		return
	}
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

	schedules, err := h.queries.ListBackupSchedules(r.Context(), sqlc.ListBackupSchedulesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list schedules")
		return
	}

	total, err := h.queries.CountBackupSchedules(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count schedules")
		return
	}

	items := make([]BackupScheduleResponse, 0, len(schedules))
	for _, s := range schedules {
		if !allow(uuid.UUID(s.ClusterID.Bytes), s.ClusterID.Valid) {
			continue
		}
		items = append(items, backupScheduleToResponse(s))
	}
	if restricted {
		total = int64(len(items))
	}
	paging.Write(w, items, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(items)))
}

// CreateScheduleRequest represents the request body for creating a backup schedule.
// openapi:request BackupScheduleRequest
type CreateScheduleRequest struct {
	Name               string   `json:"name" validate:"required"`
	StorageID          string   `json:"storage_id"`
	BackupType         string   `json:"backup_type"`
	CronExpression     string   `json:"cron_expression" validate:"required"`
	RetentionCount     int32    `json:"retention_count"`
	Enabled            bool     `json:"enabled"`
	ClusterID          string   `json:"cluster_id"`
	VeleroNamespace    string   `json:"velero_namespace"`
	IncludedNamespaces []string `json:"included_namespaces"`
	ExcludedNamespaces []string `json:"excluded_namespaces"`
	TTL                string   `json:"ttl"`
}

// CreateSchedule handles POST /api/v1/backups/schedules/.
func (h *BackupHandler) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	var req CreateScheduleRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	storageID, err := uuid.Parse(req.StorageID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid storage ID")
		return
	}
	storage, err := h.queries.GetBackupStorageConfigByID(r.Context(), storageID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Storage config not found")
		return
	}

	clusterID, err := h.optionalClusterID(req.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if !clusterID.Valid {
		clusterID = storage.ClusterID
	}
	if !h.authorizeBackup(w, r, clusterID, rbac.VerbCreate) {
		return
	}

	veleroNS := strings.TrimSpace(req.VeleroNamespace)
	if veleroNS == "" {
		veleroNS = storage.VeleroNamespace
	}
	if veleroNS == "" {
		veleroNS = defaultVeleroNamespace
	}
	veleroSchedName := veleroResourceName("schedule", req.Name)

	includedRaw, _ := json.Marshal(req.IncludedNamespaces)
	excludedRaw, _ := json.Marshal(req.ExcludedNamespaces)

	params := sqlc.CreateBackupScheduleParams{
		Name:               req.Name,
		StorageID:          storageID,
		BackupType:         req.BackupType,
		CronExpression:     req.CronExpression,
		RetentionCount:     req.RetentionCount,
		Enabled:            req.Enabled,
		CreatedByID:        currentUserUUID(r),
		ClusterID:          clusterID,
		VeleroNamespace:    veleroNS,
		VeleroScheduleName: veleroSchedName,
		IncludedNamespaces: includedRaw,
		ExcludedNamespaces: excludedRaw,
		Ttl:                req.TTL,
	}
	schedule, err := executeMutation(r, h.runTx,
		func(q BackupMutationTx) (sqlc.BackupSchedule, error) {
			return q.CreateBackupSchedule(r.Context(), params)
		},
		func(schedule sqlc.BackupSchedule) mutationAuditEvent {
			return mutationAuditEvent{
				action: "backup.schedule.create", resourceType: "backup_schedule", resourceID: schedule.ID.String(), resourceName: schedule.Name,
				status: http.StatusCreated, detail: map[string]any{
					"storage_id": storage.ID.String(), "cron_expression": schedule.CronExpression,
					"backup_type": schedule.BackupType, "enabled": schedule.Enabled, "retention_count": schedule.RetentionCount,
				},
			}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create schedule")
		return
	}

	if err := h.applyVeleroSchedule(r.Context(), schedule, storage); err != nil && h.log != nil {
		h.log.Warn("failed to apply velero Schedule CR", "schedule_id", schedule.ID.String(), "error", err)
	}

	h.publishBackupChanged(schedule.ClusterID, schedule.ID, "schedule")

	w.Header().Set("Location", "/api/v1/backups/schedules/"+schedule.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, backupScheduleToResponse(schedule))
}

// GetSchedule handles GET /api/v1/backups/schedules/{id}/.
func (h *BackupHandler) GetSchedule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid schedule ID")
		return
	}
	schedule, err := h.queries.GetBackupScheduleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Schedule not found")
		return
	}
	if !h.authorizeBackup(w, r, schedule.ClusterID, rbac.VerbRead) {
		return
	}
	RespondJSON(w, http.StatusOK, backupScheduleToResponse(schedule))
}

// DeleteSchedule handles DELETE /api/v1/backups/schedules/{id}/.
func (h *BackupHandler) DeleteSchedule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid schedule ID")
		return
	}

	existing, err := h.queries.GetBackupScheduleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Schedule not found")
		return
	}
	if !h.authorizeBackup(w, r, existing.ClusterID, rbac.VerbDelete) {
		return
	}
	scheduleName := existing.Name
	_, err = executeMutation(r, h.runTx,
		func(q BackupMutationTx) (sqlc.BackupSchedule, error) {
			return existing, q.DeleteBackupSchedule(r.Context(), id)
		},
		func(schedule sqlc.BackupSchedule) mutationAuditEvent {
			return mutationAuditEvent{action: "backup.schedule.delete", resourceType: "backup_schedule", resourceID: schedule.ID.String(), resourceName: scheduleName, status: http.StatusNoContent}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete schedule")
		return
	}

	h.publishBackupChanged(existing.ClusterID, id, "schedule")

	w.WriteHeader(http.StatusNoContent)
}

// UpdateSchedule handles PUT /api/v1/backups/schedules/{id}/.
func (h *BackupHandler) UpdateSchedule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid schedule ID")
		return
	}

	var req CreateScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	storageID, err := uuid.Parse(req.StorageID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid storage ID")
		return
	}
	storage, err := h.queries.GetBackupStorageConfigByID(r.Context(), storageID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Storage config not found")
		return
	}

	clusterID, err := h.optionalClusterID(req.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if !clusterID.Valid {
		clusterID = storage.ClusterID
	}

	existing, err := h.queries.GetBackupScheduleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Schedule not found")
		return
	}
	if !h.authorizeBackup(w, r, existing.ClusterID, rbac.VerbUpdate) {
		return
	}
	if clusterID.Valid && clusterID != existing.ClusterID && !h.authorizeBackup(w, r, clusterID, rbac.VerbUpdate) {
		return
	}

	veleroNS := strings.TrimSpace(req.VeleroNamespace)
	if veleroNS == "" {
		veleroNS = existing.VeleroNamespace
	}
	if veleroNS == "" {
		veleroNS = defaultVeleroNamespace
	}
	veleroSchedName := existing.VeleroScheduleName
	if veleroSchedName == "" {
		veleroSchedName = veleroResourceName("schedule", req.Name)
	}

	includedRaw, _ := json.Marshal(req.IncludedNamespaces)
	excludedRaw, _ := json.Marshal(req.ExcludedNamespaces)

	params := sqlc.UpdateBackupScheduleParams{
		ID:                 id,
		Name:               req.Name,
		StorageID:          storageID,
		BackupType:         req.BackupType,
		CronExpression:     req.CronExpression,
		RetentionCount:     req.RetentionCount,
		Enabled:            req.Enabled,
		ClusterID:          clusterID,
		VeleroNamespace:    veleroNS,
		VeleroScheduleName: veleroSchedName,
		IncludedNamespaces: includedRaw,
		ExcludedNamespaces: excludedRaw,
		Ttl:                req.TTL,
	}
	schedule, err := executeMutation(r, h.runTx,
		func(q BackupMutationTx) (sqlc.BackupSchedule, error) {
			return q.UpdateBackupSchedule(r.Context(), params)
		},
		func(schedule sqlc.BackupSchedule) mutationAuditEvent {
			return mutationAuditEvent{
				action: "backup.schedule.update", resourceType: "backup_schedule", resourceID: schedule.ID.String(), resourceName: schedule.Name,
				status: http.StatusOK, detail: map[string]any{
					"storage_id": storage.ID.String(), "cron_expression": schedule.CronExpression,
					"backup_type": schedule.BackupType, "enabled": schedule.Enabled,
				},
			}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update schedule")
		return
	}

	if err := h.applyVeleroSchedule(r.Context(), schedule, storage); err != nil && h.log != nil {
		h.log.Warn("failed to apply velero Schedule CR", "schedule_id", schedule.ID.String(), "error", err)
	}

	h.publishBackupChanged(schedule.ClusterID, schedule.ID, "schedule")

	RespondJSON(w, http.StatusOK, backupScheduleToResponse(schedule))
}

// TriggerSchedule handles POST /api/v1/backups/schedules/{id}/trigger-now/.
// Creates a one-off Velero Backup CR (named backup-{schedule-id}-{timestamp})
// and tracks it via a row in our backups table. The worker will poll the CR
// for status and roll up to our row.
func (h *BackupHandler) TriggerSchedule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid schedule ID")
		return
	}
	schedule, err := h.queries.GetBackupScheduleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Backup schedule not found")
		return
	}
	storage, err := h.queries.GetBackupStorageConfigByID(r.Context(), schedule.StorageID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Storage config not found")
		return
	}

	now := time.Now().UTC()
	veleroBackupName := fmt.Sprintf("backup-%s-%s", id.String()[:8], now.Format("20060102t150405"))
	clusterID := schedule.ClusterID
	if !clusterID.Valid {
		clusterID = storage.ClusterID
	}
	// Triggering creates a real backup run against the cluster — gate as create.
	if !h.authorizeBackup(w, r, clusterID, rbac.VerbCreate) {
		return
	}
	veleroNS := schedule.VeleroNamespace
	if veleroNS == "" {
		veleroNS = storage.VeleroNamespace
	}
	if veleroNS == "" {
		veleroNS = defaultVeleroNamespace
	}

	params := sqlc.CreateBackupParams{
		Name:               schedule.Name + " (manual trigger)",
		StorageID:          schedule.StorageID,
		BackupType:         schedule.BackupType,
		Status:             "pending",
		DatabaseTables:     json.RawMessage(`[]`),
		CreatedByID:        currentUserUUID(r),
		ClusterID:          clusterID,
		VeleroBackupName:   veleroBackupName,
		VeleroNamespace:    veleroNS,
		IncludedNamespaces: schedule.IncludedNamespaces,
		ExcludedNamespaces: schedule.ExcludedNamespaces,
	}
	backup, err := executeMutation(r, h.runTx,
		func(q BackupMutationTx) (sqlc.Backup, error) { return q.CreateBackup(r.Context(), params) },
		func(backup sqlc.Backup) mutationAuditEvent {
			return mutationAuditEvent{
				action: "backup.schedule.trigger", resourceType: "backup_schedule", resourceID: schedule.ID.String(), resourceName: schedule.Name,
				status: http.StatusCreated, detail: map[string]any{"backup_id": backup.ID.String(), "backup_name": backup.Name},
			}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to trigger backup")
		return
	}

	if err := h.applyVeleroBackupForRow(r.Context(), backup, storage); err != nil && h.log != nil {
		h.log.Warn("failed to apply manual velero Backup CR", "backup_id", backup.ID.String(), "error", err)
	}

	h.publishBackupChanged(backup.ClusterID, backup.ID, "backup")

	w.Header().Set("Location", "/api/v1/backups/"+backup.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, backupToResponse(backup))
}

// --- Restore ---
