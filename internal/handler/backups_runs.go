package handler

import (
	"encoding/json"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// ListBackups handles GET /api/v1/backups/.
func (h *BackupHandler) ListBackups(w http.ResponseWriter, r *http.Request) {
	allow, restricted, ok := h.authz.resourceScopeFilter(w, r, rbac.ResourceBackups, rbac.VerbRead)
	if !ok {
		return
	}
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

	backups, err := h.queries.ListBackups(r.Context(), sqlc.ListBackupsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list backups")
		return
	}

	total, err := h.queries.CountBackups(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count backups")
		return
	}

	items := make([]BackupResponse, 0, len(backups))
	for _, b := range backups {
		if !allow(uuid.UUID(b.ClusterID.Bytes), b.ClusterID.Valid) {
			continue
		}
		items = append(items, backupToResponse(b))
	}
	if restricted {
		total = int64(len(items))
	}
	paging.Write(w, items, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(items)))
}

// CreateBackupRequest represents the request body for creating a backup.
// openapi:request BackupCreateRequest
type CreateBackupRequest struct {
	Name               string          `json:"name" validate:"required"`
	StorageID          string          `json:"storage_id"`
	BackupType         string          `json:"backup_type"`
	DatabaseTables     json.RawMessage `json:"database_tables"`
	IncludedNamespaces []string        `json:"included_namespaces"`
	ExcludedNamespaces []string        `json:"excluded_namespaces"`
}

// CreateBackup handles POST /api/v1/backups/.
func (h *BackupHandler) CreateBackup(w http.ResponseWriter, r *http.Request) {
	var req CreateBackupRequest
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
	if !h.authorizeBackup(w, r, storage.ClusterID, rbac.VerbCreate) {
		return
	}

	if req.DatabaseTables == nil {
		req.DatabaseTables = json.RawMessage(`[]`)
	}

	veleroNS := storage.VeleroNamespace
	if veleroNS == "" {
		veleroNS = defaultVeleroNamespace
	}
	veleroBackupName := veleroResourceName("backup", req.Name)

	included, _ := json.Marshal(req.IncludedNamespaces)
	excluded, _ := json.Marshal(req.ExcludedNamespaces)

	params := sqlc.CreateBackupParams{
		Name:               req.Name,
		StorageID:          storageID,
		BackupType:         req.BackupType,
		Status:             "pending",
		DatabaseTables:     req.DatabaseTables,
		CreatedByID:        currentUserUUID(r),
		ClusterID:          storage.ClusterID,
		VeleroBackupName:   veleroBackupName,
		VeleroNamespace:    veleroNS,
		IncludedNamespaces: included,
		ExcludedNamespaces: excluded,
	}
	backup, err := executeMutation(r, h.runTx,
		func(q BackupMutationTx) (sqlc.Backup, error) { return q.CreateBackup(r.Context(), params) },
		func(backup sqlc.Backup) mutationAuditEvent {
			return mutationAuditEvent{
				action: "backup.create", resourceType: "backup", resourceID: backup.ID.String(), resourceName: backup.Name,
				status: http.StatusCreated, detail: map[string]any{
					"storage_id": storage.ID.String(), "backup_type": backup.BackupType, "on_demand": true,
				},
			}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create backup")
		return
	}

	// Best-effort fire-and-forget: apply the Velero Backup CR. The worker
	// task picks it up if this server-side apply fails or no agent is
	// connected at this instant.
	if err := h.applyVeleroBackupForRow(r.Context(), backup, storage); err != nil && h.log != nil {
		h.log.Warn("failed to apply velero backup CR", "backup_id", backup.ID.String(), "error", err)
	}

	h.publishBackupChanged(backup.ClusterID, backup.ID, "backup")

	w.Header().Set("Location", "/api/v1/backups/"+backup.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, backupToResponse(backup))
}

// GetBackup handles GET /api/v1/backups/{id}/.
func (h *BackupHandler) GetBackup(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Backup not found")
		return
	}

	backup, err := h.queries.GetBackupByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Backup not found")
		return
	}
	if !h.authorizeBackup(w, r, backup.ClusterID, rbac.VerbRead) {
		return
	}

	RespondJSON(w, http.StatusOK, backupToResponse(backup))
}

// DeleteBackup handles DELETE /api/v1/backups/{id}/.
func (h *BackupHandler) DeleteBackup(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid backup ID")
		return
	}
	existing, err := h.queries.GetBackupByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Backup not found")
		return
	}
	if !h.authorizeBackup(w, r, existing.ClusterID, rbac.VerbDelete) {
		return
	}
	backupName := existing.Name
	_, err = executeMutation(r, h.runTx,
		func(q BackupMutationTx) (sqlc.Backup, error) { return existing, q.DeleteBackup(r.Context(), id) },
		func(backup sqlc.Backup) mutationAuditEvent {
			return mutationAuditEvent{action: "backup.delete", resourceType: "backup", resourceID: backup.ID.String(), resourceName: backupName, status: http.StatusNoContent}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete backup")
		return
	}
	h.publishBackupChanged(existing.ClusterID, id, "backup")
	w.WriteHeader(http.StatusNoContent)
}

// --- Schedules ---
