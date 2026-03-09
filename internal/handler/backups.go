package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// BackupQuerier abstracts the backup-related database queries needed by BackupHandler.
type BackupQuerier interface {
	// Storage configs
	GetBackupStorageConfigByID(ctx context.Context, id uuid.UUID) (sqlc.BackupStorageConfig, error)
	ListBackupStorageConfigs(ctx context.Context, arg sqlc.ListBackupStorageConfigsParams) ([]sqlc.BackupStorageConfig, error)
	CreateBackupStorageConfig(ctx context.Context, arg sqlc.CreateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error)
	DeleteBackupStorageConfig(ctx context.Context, id uuid.UUID) error
	CountBackupStorageConfigs(ctx context.Context) (int64, error)
	// Backups
	ListBackups(ctx context.Context, arg sqlc.ListBackupsParams) ([]sqlc.Backup, error)
	GetBackupByID(ctx context.Context, id uuid.UUID) (sqlc.Backup, error)
	CreateBackup(ctx context.Context, arg sqlc.CreateBackupParams) (sqlc.Backup, error)
	CountBackups(ctx context.Context) (int64, error)
	// Schedules
	ListBackupSchedules(ctx context.Context, arg sqlc.ListBackupSchedulesParams) ([]sqlc.BackupSchedule, error)
	GetBackupScheduleByID(ctx context.Context, id uuid.UUID) (sqlc.BackupSchedule, error)
	CreateBackupSchedule(ctx context.Context, arg sqlc.CreateBackupScheduleParams) (sqlc.BackupSchedule, error)
	DeleteBackupSchedule(ctx context.Context, id uuid.UUID) error
	CountBackupSchedules(ctx context.Context) (int64, error)
	// Restore
	ListRestoreOperations(ctx context.Context, arg sqlc.ListRestoreOperationsParams) ([]sqlc.RestoreOperation, error)
	CreateRestoreOperation(ctx context.Context, arg sqlc.CreateRestoreOperationParams) (sqlc.RestoreOperation, error)
	CountRestoreOperations(ctx context.Context) (int64, error)
}

// BackupHandler handles backup endpoints (storage configs, backups, schedules, restores).
type BackupHandler struct {
	queries BackupQuerier
}

// NewBackupHandler creates a new backup handler.
func NewBackupHandler(queries BackupQuerier) *BackupHandler {
	return &BackupHandler{queries: queries}
}

// --- Storage Configs ---

// ListStorageConfigs handles GET /api/v1/backups/storage/.
func (h *BackupHandler) ListStorageConfigs(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	configs, err := h.queries.ListBackupStorageConfigs(r.Context(), sqlc.ListBackupStorageConfigsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list storage configs")
		return
	}

	total, err := h.queries.CountBackupStorageConfigs(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count storage configs")
		return
	}

	RespondPaginated(w, r, configs, total)
}

// CreateStorageConfigRequest represents the request body for creating a storage config.
type CreateStorageConfigRequest struct {
	Name        string `json:"name"`
	StorageType string `json:"storage_type"`
	Bucket      string `json:"bucket"`
	Prefix      string `json:"prefix"`
	Region      string `json:"region"`
	EndpointURL string `json:"endpoint_url"`
	AccessKey   string `json:"access_key"`
	SecretKey   string `json:"secret_key"`
	IsDefault   bool   `json:"is_default"`
}

// CreateStorageConfig handles POST /api/v1/backups/storage/.
func (h *BackupHandler) CreateStorageConfig(w http.ResponseWriter, r *http.Request) {
	var req CreateStorageConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Storage config name is required")
		return
	}
	if req.Bucket == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Bucket is required")
		return
	}

	config, err := h.queries.CreateBackupStorageConfig(r.Context(), sqlc.CreateBackupStorageConfigParams{
		Name:        req.Name,
		StorageType: req.StorageType,
		Bucket:      req.Bucket,
		Prefix:      req.Prefix,
		Region:      req.Region,
		EndpointUrl: req.EndpointURL,
		AccessKey:   req.AccessKey,
		SecretKey:   req.SecretKey,
		IsDefault:   req.IsDefault,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create storage config")
		return
	}

	RespondJSON(w, http.StatusCreated, config)
}

// GetStorageConfig handles GET /api/v1/backups/storage/{id}/.
func (h *BackupHandler) GetStorageConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid storage config ID")
		return
	}

	config, err := h.queries.GetBackupStorageConfigByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Storage config not found")
		return
	}

	RespondJSON(w, http.StatusOK, config)
}

// DeleteStorageConfig handles DELETE /api/v1/backups/storage/{id}/.
func (h *BackupHandler) DeleteStorageConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid storage config ID")
		return
	}

	if err := h.queries.DeleteBackupStorageConfig(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "delete_error", "Failed to delete storage config")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Backups ---

// ListBackups handles GET /api/v1/backups/.
func (h *BackupHandler) ListBackups(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	backups, err := h.queries.ListBackups(r.Context(), sqlc.ListBackupsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list backups")
		return
	}

	total, err := h.queries.CountBackups(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count backups")
		return
	}

	RespondPaginated(w, r, backups, total)
}

// CreateBackupRequest represents the request body for creating a backup.
type CreateBackupRequest struct {
	Name           string          `json:"name"`
	StorageID      string          `json:"storage_id"`
	BackupType     string          `json:"backup_type"`
	DatabaseTables json.RawMessage `json:"database_tables"`
}

// CreateBackup handles POST /api/v1/backups/.
func (h *BackupHandler) CreateBackup(w http.ResponseWriter, r *http.Request) {
	var req CreateBackupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Backup name is required")
		return
	}

	storageID, err := uuid.Parse(req.StorageID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid storage ID")
		return
	}

	if req.DatabaseTables == nil {
		req.DatabaseTables = json.RawMessage(`[]`)
	}

	backup, err := h.queries.CreateBackup(r.Context(), sqlc.CreateBackupParams{
		Name:           req.Name,
		StorageID:      storageID,
		BackupType:     req.BackupType,
		Status:         "pending",
		DatabaseTables: req.DatabaseTables,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create backup")
		return
	}

	RespondJSON(w, http.StatusCreated, backup)
}

// GetBackup handles GET /api/v1/backups/{id}/.
func (h *BackupHandler) GetBackup(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid backup ID")
		return
	}

	backup, err := h.queries.GetBackupByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Backup not found")
		return
	}

	RespondJSON(w, http.StatusOK, backup)
}

// --- Schedules ---

// ListSchedules handles GET /api/v1/backups/schedules/.
func (h *BackupHandler) ListSchedules(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	schedules, err := h.queries.ListBackupSchedules(r.Context(), sqlc.ListBackupSchedulesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list schedules")
		return
	}

	total, err := h.queries.CountBackupSchedules(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count schedules")
		return
	}

	RespondPaginated(w, r, schedules, total)
}

// CreateScheduleRequest represents the request body for creating a backup schedule.
type CreateScheduleRequest struct {
	Name           string `json:"name"`
	StorageID      string `json:"storage_id"`
	BackupType     string `json:"backup_type"`
	CronExpression string `json:"cron_expression"`
	RetentionCount int32  `json:"retention_count"`
	Enabled        bool   `json:"enabled"`
}

// CreateSchedule handles POST /api/v1/backups/schedules/.
func (h *BackupHandler) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	var req CreateScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Schedule name is required")
		return
	}
	if req.CronExpression == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Cron expression is required")
		return
	}

	storageID, err := uuid.Parse(req.StorageID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid storage ID")
		return
	}

	schedule, err := h.queries.CreateBackupSchedule(r.Context(), sqlc.CreateBackupScheduleParams{
		Name:           req.Name,
		StorageID:      storageID,
		BackupType:     req.BackupType,
		CronExpression: req.CronExpression,
		RetentionCount: req.RetentionCount,
		Enabled:        req.Enabled,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create schedule")
		return
	}

	RespondJSON(w, http.StatusCreated, schedule)
}

// DeleteSchedule handles DELETE /api/v1/backups/schedules/{id}/.
func (h *BackupHandler) DeleteSchedule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid schedule ID")
		return
	}

	if err := h.queries.DeleteBackupSchedule(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "delete_error", "Failed to delete schedule")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Restore ---

// CreateRestoreRequest represents the request body for creating a restore operation.
type CreateRestoreRequest struct{}

// CreateRestore handles POST /api/v1/backups/{id}/restore/.
func (h *BackupHandler) CreateRestore(w http.ResponseWriter, r *http.Request) {
	backupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid backup ID")
		return
	}

	// Verify the backup exists.
	_, err = h.queries.GetBackupByID(r.Context(), backupID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Backup not found")
		return
	}

	restore, err := h.queries.CreateRestoreOperation(r.Context(), sqlc.CreateRestoreOperationParams{
		BackupID:      backupID,
		Status:        "pending",
		InitiatedByID: pgtype.UUID{},
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create restore operation")
		return
	}

	RespondJSON(w, http.StatusCreated, restore)
}
