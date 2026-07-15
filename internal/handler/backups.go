package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// BackupQuerier abstracts the backup-related database queries needed by BackupHandler.
type BackupQuerier interface {
	// Storage configs
	GetBackupStorageConfigByID(ctx context.Context, id uuid.UUID) (sqlc.BackupStorageConfig, error)
	ListBackupStorageConfigs(ctx context.Context, arg sqlc.ListBackupStorageConfigsParams) ([]sqlc.BackupStorageConfig, error)
	CreateBackupStorageConfig(ctx context.Context, arg sqlc.CreateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error)
	UpdateBackupStorageConfig(ctx context.Context, arg sqlc.UpdateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error)
	DeleteBackupStorageConfig(ctx context.Context, id uuid.UUID) error
	CountBackupStorageConfigs(ctx context.Context) (int64, error)
	// Backups
	ListBackups(ctx context.Context, arg sqlc.ListBackupsParams) ([]sqlc.Backup, error)
	ListRunningBackupsForPolling(ctx context.Context, limit int32) ([]sqlc.Backup, error)
	GetBackupByID(ctx context.Context, id uuid.UUID) (sqlc.Backup, error)
	CreateBackup(ctx context.Context, arg sqlc.CreateBackupParams) (sqlc.Backup, error)
	UpdateBackupVeleroIdentity(ctx context.Context, arg sqlc.UpdateBackupVeleroIdentityParams) error
	// UpdateBackupStarted is a CAS claim: rows==0 means another replica already
	// claimed or the row left pending/queued/created.
	UpdateBackupStarted(ctx context.Context, id uuid.UUID) (int64, error)
	UpdateBackupCompleted(ctx context.Context, arg sqlc.UpdateBackupCompletedParams) error
	UpdateBackupFailed(ctx context.Context, arg sqlc.UpdateBackupFailedParams) error
	TouchBackupPolling(ctx context.Context, id uuid.UUID) error
	DeleteBackup(ctx context.Context, id uuid.UUID) error
	CountBackups(ctx context.Context) (int64, error)
	// Schedules
	ListBackupSchedules(ctx context.Context, arg sqlc.ListBackupSchedulesParams) ([]sqlc.BackupSchedule, error)
	GetBackupScheduleByID(ctx context.Context, id uuid.UUID) (sqlc.BackupSchedule, error)
	CreateBackupSchedule(ctx context.Context, arg sqlc.CreateBackupScheduleParams) (sqlc.BackupSchedule, error)
	UpdateBackupSchedule(ctx context.Context, arg sqlc.UpdateBackupScheduleParams) (sqlc.BackupSchedule, error)
	DeleteBackupSchedule(ctx context.Context, id uuid.UUID) error
	CountBackupSchedules(ctx context.Context) (int64, error)
	// Restore
	ListRestoreOperations(ctx context.Context, arg sqlc.ListRestoreOperationsParams) ([]sqlc.RestoreOperation, error)
	ListRunningRestoresForPolling(ctx context.Context, limit int32) ([]sqlc.RestoreOperation, error)
	GetRestoreOperationByID(ctx context.Context, id uuid.UUID) (sqlc.RestoreOperation, error)
	CreateRestoreOperation(ctx context.Context, arg sqlc.CreateRestoreOperationParams) (sqlc.RestoreOperation, error)
	UpdateRestoreOperationStarted(ctx context.Context, id uuid.UUID) (int64, error)
	UpdateRestoreOperationCompleted(ctx context.Context, id uuid.UUID) error
	UpdateRestoreOperationFailed(ctx context.Context, arg sqlc.UpdateRestoreOperationFailedParams) error
	TouchRestorePolling(ctx context.Context, id uuid.UUID) error
	CountRestoreOperations(ctx context.Context) (int64, error)
}

// BackupHandler handles backup endpoints (storage configs, backups, schedules, restores).
//
// Phase B2 wires Velero as the engine: the row in our DB is the source of
// desired state but never the source of truth for completion — that lives on
// the Velero CRs in each cluster, which we round-trip through the existing
// tunnel K8sRequester.
type BackupHandler struct {
	queries    BackupQuerier
	encryptor  *auth.Encryptor
	requester  K8sRequester
	httpClient *http.Client
	log        *slog.Logger
	authz      authorizationSupport
	bus        *events.Bus
}

// NewBackupHandler creates a new backup handler.
func NewBackupHandler(queries BackupQuerier) *BackupHandler {
	return &BackupHandler{
		queries:    queries,
		log:        slog.Default(),
		// SEC-03: S3 connectivity probe dials operator-supplied endpoints;
		// SafeClient enforces public-IP at dial time (not GuardPublicHost alone).
		httpClient: httpclient.SafeClient(15 * time.Second),
	}
}

// SetEventBus wires the SSE bus for backup.changed liveness events (P4.5).
// Optional: publishers are fire-and-forget and nil-safe.
func (h *BackupHandler) SetEventBus(bus *events.Bus) {
	if h == nil {
		return
	}
	h.bus = bus
}

// publishBackupChanged emits the metadata-only backup.changed event after a
// successful DB write. kind discriminates backup|restore|schedule.
func (h *BackupHandler) publishBackupChanged(clusterID pgtype.UUID, id uuid.UUID, kind string) {
	if h == nil {
		return
	}
	events.PublishChanged(h.bus, "backup", nullableUUIDString(clusterID), id.String(), map[string]any{"kind": kind})
}

// SetAuthorization wires the RBAC engine + binding querier used to enforce
// ResourceBackups permission on every handler. Until this is called the handler
// fails closed for authenticated callers (bindingsForContext returns a
// not-configured error → 500) rather than allowing unauthenticated access.
func (h *BackupHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	if h == nil {
		return
	}
	h.authz.SetAuthorization(engine, querier)
}

// authorizeBackup gates an action against ResourceBackups. Cluster-scoped rows
// are checked against the caller's grant on that cluster; unscoped (global) rows
// require a global backups grant. It writes the error response and returns false
// when the caller is not permitted.
func (h *BackupHandler) authorizeBackup(w http.ResponseWriter, r *http.Request, clusterID pgtype.UUID, verb rbac.Verb) bool {
	if clusterID.Valid {
		return h.authz.authorizeClusterAction(w, r, uuid.UUID(clusterID.Bytes), rbac.ResourceBackups, verb)
	}
	return h.authz.authorizeGlobalAction(w, r, rbac.ResourceBackups, verb)
}

// SetEncryptor wires the Fernet encryptor used to round-trip cloud credentials
// into the BackupStorageConfig.encrypted_credentials column. When nil, raw
// access_key/secret_key (legacy plaintext) are used as a fallback so dev
// environments without ASTRONOMER_ENCRYPTION_KEY still function.
func (h *BackupHandler) SetEncryptor(e *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = e
}

// SetK8sRequester wires the tunnel-backed Kubernetes API proxy. Without it,
// the handler runs in degraded mode: storage/schedule/backup/restore writes
// still hit our DB but no Velero CR is applied. This keeps the test surface
// usable when no agent is connected.
func (h *BackupHandler) SetK8sRequester(r K8sRequester) {
	if h == nil {
		return
	}
	h.requester = r
}

// SetHTTPClient overrides the HTTP client used by TestStorageConfig for
// connectivity probes. Tests use httptest.NewServer + its Client to avoid
// hitting the live network.
func (h *BackupHandler) SetHTTPClient(client *http.Client) {
	if h == nil || client == nil {
		return
	}
	h.httpClient = client
}

// SetLogger overrides the structured logger used by this handler.
func (h *BackupHandler) SetLogger(log *slog.Logger) {
	if h == nil || log == nil {
		return
	}
	h.log = log
}

// ControllerStatus summarizes backup subsystem operational state.
func (h *BackupHandler) ControllerStatus(w http.ResponseWriter, r *http.Request) {
	// Aggregates every cluster's backup state, so it needs a global read grant.
	if !h.authz.authorizeGlobalAction(w, r, rbac.ResourceBackups, rbac.VerbRead) {
		return
	}
	summary, err := h.controllerSummary(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to load backups")
		return
	}
	RespondJSON(w, http.StatusOK, summary)
}

func (h *BackupHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	backups, err := h.queries.ListBackups(ctx, sqlc.ListBackupsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	schedules, err := h.queries.ListBackupSchedules(ctx, sqlc.ListBackupSchedulesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	restores, err := h.queries.ListRestoreOperations(ctx, sqlc.ListRestoreOperationsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	storageCount, _ := h.queries.CountBackupStorageConfigs(ctx)
	backupCounts := map[string]int{}
	restoreCounts := map[string]int{}
	runningBackups := 0
	runningRestores := 0
	failedBackups := 0
	failedRestores := 0
	for _, backup := range backups {
		backupCounts[backup.Status]++
		switch backup.Status {
		case "pending", "running", "in_progress":
			runningBackups++
		case "failed", "error":
			failedBackups++
		}
	}
	enabledSchedules := 0
	for _, schedule := range schedules {
		if schedule.Enabled {
			enabledSchedules++
		}
	}
	for _, restore := range restores {
		restoreCounts[restore.Status]++
		switch restore.Status {
		case "pending", "running", "in_progress":
			runningRestores++
		case "failed", "error":
			failedRestores++
		}
	}
	health := "healthy"
	reasons := make([]string, 0, 2)
	if failedBackups > 0 {
		health = "degraded"
		reasons = append(reasons, "failed_backups_present")
	}
	if failedRestores > 0 {
		health = "degraded"
		reasons = append(reasons, "failed_restores_present")
	}
	return map[string]any{
		"reconciler": map[string]any{
			"enabled": h.requester != nil,
			"engine":  "velero",
		},
		"health":        health,
		"healthReasons": reasons,
		"storage": map[string]any{
			"count": storageCount,
		},
		"backups": map[string]any{
			"total":        len(backups),
			"runningCount": runningBackups,
			"failedCount":  failedBackups,
			"statuses":     backupCounts,
		},
		"schedules": map[string]any{
			"total":        len(schedules),
			"enabledCount": enabledSchedules,
		},
		"restores": map[string]any{
			"total":        len(restores),
			"runningCount": runningRestores,
			"failedCount":  failedRestores,
		},
	}, nil
}

// --- Storage Configs ---

// ListStorageConfigs handles GET /api/v1/backups/storage/.
func (h *BackupHandler) ListStorageConfigs(w http.ResponseWriter, r *http.Request) {
	allow, restricted, ok := h.authz.resourceScopeFilter(w, r, rbac.ResourceBackups, rbac.VerbRead)
	if !ok {
		return
	}
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))

	configs, err := h.queries.ListBackupStorageConfigs(r.Context(), sqlc.ListBackupStorageConfigsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list storage configs")
		return
	}

	total, err := h.queries.CountBackupStorageConfigs(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count storage configs")
		return
	}

	out := make([]map[string]any, 0, len(configs))
	for _, c := range configs {
		if !allow(uuid.UUID(c.ClusterID.Bytes), c.ClusterID.Valid) {
			continue
		}
		out = append(out, h.storageResponse(c))
	}
	if restricted {
		total = int64(len(out))
	}
	RespondPaginated(w, r, out, total)
}

// CreateStorageConfigRequest represents the request body for creating a storage config.
type CreateStorageConfigRequest struct {
	Name            string `json:"name" validate:"required"`
	StorageType     string `json:"storage_type"`
	Bucket          string `json:"bucket" validate:"required"`
	Prefix          string `json:"prefix"`
	Region          string `json:"region"`
	EndpointURL     string `json:"endpoint_url"`
	AccessKey       string `json:"access_key"`
	SecretKey       string `json:"secret_key"`
	IsDefault       bool   `json:"is_default"`
	ClusterID       string `json:"cluster_id"`
	VeleroNamespace string `json:"velero_namespace"`
	BSLName         string `json:"bsl_name"`
}

// CreateStorageConfig handles POST /api/v1/backups/storage/.
func (h *BackupHandler) CreateStorageConfig(w http.ResponseWriter, r *http.Request) {
	var req CreateStorageConfigRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	clusterID, err := h.optionalClusterID(req.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if !h.authorizeBackup(w, r, clusterID, rbac.VerbCreate) {
		return
	}

	encrypted, err := h.encryptCredentials(req.AccessKey, req.SecretKey)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CryptoError, "Failed to encrypt credentials")
		return
	}
	legacyAccessKey, legacySecretKey := legacyBackupCredentialColumns(req.AccessKey, req.SecretKey, encrypted)

	veleroNS := strings.TrimSpace(req.VeleroNamespace)
	if veleroNS == "" {
		veleroNS = defaultVeleroNamespace
	}
	bslName := strings.TrimSpace(req.BSLName)

	config, err := h.queries.CreateBackupStorageConfig(r.Context(), sqlc.CreateBackupStorageConfigParams{
		Name:                 req.Name,
		StorageType:          req.StorageType,
		Bucket:               req.Bucket,
		Prefix:               req.Prefix,
		Region:               req.Region,
		EndpointUrl:          req.EndpointURL,
		AccessKey:            legacyAccessKey,
		SecretKey:            legacySecretKey,
		IsDefault:            req.IsDefault,
		CreatedByID:          currentUserUUID(r),
		ClusterID:            clusterID,
		VeleroNamespace:      veleroNS,
		BslName:              bslName,
		EncryptedCredentials: encrypted,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create storage config")
		return
	}
	if config.BslName == "" {
		// Fill in a stable, slug-derived name now we know the row's UUID.
		config.BslName = veleroBSLNameFor(config)
		// Persist by re-issuing an update with all columns. Best-effort; failures
		// here only affect the next round-trip and are logged.
		if _, uerr := h.queries.UpdateBackupStorageConfig(r.Context(), buildStorageUpdateParams(config)); uerr != nil && h.log != nil {
			h.log.Warn("failed to persist BSL name", "config_id", config.ID.String(), "error", uerr)
		}
	}

	if err := h.applyVeleroBSL(r.Context(), config, req.AccessKey, req.SecretKey); err != nil && h.log != nil {
		h.log.Warn("failed to apply velero BSL", "config_id", config.ID.String(), "error", err)
	}

	recordAudit(r, h.queries, "backup.storage.create", "backup_storage_config", config.ID.String(), config.Name, map[string]any{
		"storage_type": config.StorageType,
		"bucket":       config.Bucket,
		"region":       config.Region,
		"is_default":   config.IsDefault,
	})

	w.Header().Set("Location", "/api/v1/backups/storage/"+config.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, h.storageResponse(config))
}

// GetStorageConfig handles GET /api/v1/backups/storage/{id}/.
func (h *BackupHandler) GetStorageConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid storage config ID")
		return
	}

	config, err := h.queries.GetBackupStorageConfigByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Storage config not found")
		return
	}
	if !h.authorizeBackup(w, r, config.ClusterID, rbac.VerbRead) {
		return
	}

	RespondJSON(w, http.StatusOK, h.storageResponse(config))
}

// DeleteStorageConfig handles DELETE /api/v1/backups/storage/{id}/.
func (h *BackupHandler) DeleteStorageConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid storage config ID")
		return
	}

	// Look up before delete so we can authorize against the row's cluster and
	// put the friendly name in the audit row.
	existing, err := h.queries.GetBackupStorageConfigByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Storage config not found")
		return
	}
	if !h.authorizeBackup(w, r, existing.ClusterID, rbac.VerbDelete) {
		return
	}
	configName := existing.Name
	if err := h.queries.DeleteBackupStorageConfig(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete storage config")
		return
	}

	recordAudit(r, h.queries, "backup.storage.delete", "backup_storage_config", id.String(), configName, nil)

	w.WriteHeader(http.StatusNoContent)
}

// UpdateStorageConfig handles PUT /api/v1/backups/storage/{id}/.
func (h *BackupHandler) UpdateStorageConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid storage config ID")
		return
	}

	var req CreateStorageConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}

	clusterID, err := h.optionalClusterID(req.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}

	// Authorize against both the existing row's cluster and the requested target
	// cluster, so a caller cannot edit a config they don't control or re-home it
	// onto a cluster they lack permission for.
	existing, err := h.queries.GetBackupStorageConfigByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Storage config not found")
		return
	}
	if !h.authorizeBackup(w, r, existing.ClusterID, rbac.VerbUpdate) {
		return
	}
	if clusterID.Valid && clusterID != existing.ClusterID && !h.authorizeBackup(w, r, clusterID, rbac.VerbUpdate) {
		return
	}

	encrypted, err := h.encryptCredentials(req.AccessKey, req.SecretKey)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CryptoError, "Failed to encrypt credentials")
		return
	}
	legacyAccessKey, legacySecretKey := legacyBackupCredentialColumns(req.AccessKey, req.SecretKey, encrypted)

	veleroNS := strings.TrimSpace(req.VeleroNamespace)
	if veleroNS == "" {
		veleroNS = defaultVeleroNamespace
	}
	bslName := strings.TrimSpace(req.BSLName)

	config, err := h.queries.UpdateBackupStorageConfig(r.Context(), sqlc.UpdateBackupStorageConfigParams{
		ID:                   id,
		Name:                 req.Name,
		StorageType:          req.StorageType,
		Bucket:               req.Bucket,
		Prefix:               req.Prefix,
		Region:               req.Region,
		EndpointUrl:          req.EndpointURL,
		AccessKey:            legacyAccessKey,
		SecretKey:            legacySecretKey,
		IsDefault:            req.IsDefault,
		ClusterID:            clusterID,
		VeleroNamespace:      veleroNS,
		BslName:              bslName,
		EncryptedCredentials: encrypted,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update storage config")
		return
	}

	if err := h.applyVeleroBSL(r.Context(), config, req.AccessKey, req.SecretKey); err != nil && h.log != nil {
		h.log.Warn("failed to apply velero BSL", "config_id", config.ID.String(), "error", err)
	}

	recordAudit(r, h.queries, "backup.storage.update", "backup_storage_config", config.ID.String(), config.Name, map[string]any{
		"storage_type": config.StorageType,
		"bucket":       config.Bucket,
		"region":       config.Region,
		"is_default":   config.IsDefault,
	})

	RespondJSON(w, http.StatusOK, h.storageResponse(config))
}

// TestStorageConfig handles POST /api/v1/backups/storage/{id}/test/.
//
// Real test: we issue an authenticated AWS Signature V4 GET against
// {endpoint}/{bucket}/?list-type=2&max-keys=1 using the row's credentials.
// 200 / 204 / 404 (NoSuchBucket is differentiated below) is treated as
// "credentials work, server reachable"; other errors propagate.
func (h *BackupHandler) TestStorageConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid storage config ID")
		return
	}
	cfg, err := h.queries.GetBackupStorageConfigByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Storage config not found")
		return
	}
	if !h.authorizeBackup(w, r, cfg.ClusterID, rbac.VerbUpdate) {
		return
	}
	access, secret, err := h.decryptCredentials(cfg)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CryptoError, "Failed to decrypt credentials")
		return
	}
	if err := h.probeS3Bucket(r.Context(), cfg, access, secret); err != nil {
		recordAudit(r, h.queries, "backup.storage.test", "backup_storage_config", cfg.ID.String(), cfg.Name, map[string]any{
			"success": false,
			"reason":  err.Error(),
		})
		RespondJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	recordAudit(r, h.queries, "backup.storage.test", "backup_storage_config", cfg.ID.String(), cfg.Name, map[string]any{"success": true})
	RespondJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Backup storage configuration is reachable and credentials are valid",
	})
}

// --- Backups ---

// ListBackups handles GET /api/v1/backups/.
func (h *BackupHandler) ListBackups(w http.ResponseWriter, r *http.Request) {
	allow, restricted, ok := h.authz.resourceScopeFilter(w, r, rbac.ResourceBackups, rbac.VerbRead)
	if !ok {
		return
	}
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))

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
	RespondPaginated(w, r, items, total)
}

// CreateBackupRequest represents the request body for creating a backup.
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

	backup, err := h.queries.CreateBackup(r.Context(), sqlc.CreateBackupParams{
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
	recordAudit(r, h.queries, "backup.create", "backup", backup.ID.String(), backup.Name, map[string]any{
		"storage_id":  storage.ID.String(),
		"backup_type": backup.BackupType,
		"on_demand":   true,
	})

	w.Header().Set("Location", "/api/v1/backups/"+backup.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, backupToResponse(backup))
}

// GetBackup handles GET /api/v1/backups/{id}/.
func (h *BackupHandler) GetBackup(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid backup ID")
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
	if err := h.queries.DeleteBackup(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete backup")
		return
	}
	h.publishBackupChanged(existing.ClusterID, id, "backup")
	recordAudit(r, h.queries, "backup.delete", "backup", id.String(), backupName, nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- Schedules ---

// ListSchedules handles GET /api/v1/backups/schedules/.
func (h *BackupHandler) ListSchedules(w http.ResponseWriter, r *http.Request) {
	allow, restricted, ok := h.authz.resourceScopeFilter(w, r, rbac.ResourceBackups, rbac.VerbRead)
	if !ok {
		return
	}
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))

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
	RespondPaginated(w, r, items, total)
}

// CreateScheduleRequest represents the request body for creating a backup schedule.
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

	schedule, err := h.queries.CreateBackupSchedule(r.Context(), sqlc.CreateBackupScheduleParams{
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
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create schedule")
		return
	}

	if err := h.applyVeleroSchedule(r.Context(), schedule, storage); err != nil && h.log != nil {
		h.log.Warn("failed to apply velero Schedule CR", "schedule_id", schedule.ID.String(), "error", err)
	}

	h.publishBackupChanged(schedule.ClusterID, schedule.ID, "schedule")
	recordAudit(r, h.queries, "backup.schedule.create", "backup_schedule", schedule.ID.String(), schedule.Name, map[string]any{
		"storage_id":      storage.ID.String(),
		"cron_expression": schedule.CronExpression,
		"backup_type":     schedule.BackupType,
		"enabled":         schedule.Enabled,
		"retention_count": schedule.RetentionCount,
	})

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
	if err := h.queries.DeleteBackupSchedule(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete schedule")
		return
	}

	h.publishBackupChanged(existing.ClusterID, id, "schedule")
	recordAudit(r, h.queries, "backup.schedule.delete", "backup_schedule", id.String(), scheduleName, nil)

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

	schedule, err := h.queries.UpdateBackupSchedule(r.Context(), sqlc.UpdateBackupScheduleParams{
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
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update schedule")
		return
	}

	if err := h.applyVeleroSchedule(r.Context(), schedule, storage); err != nil && h.log != nil {
		h.log.Warn("failed to apply velero Schedule CR", "schedule_id", schedule.ID.String(), "error", err)
	}

	h.publishBackupChanged(schedule.ClusterID, schedule.ID, "schedule")
	recordAudit(r, h.queries, "backup.schedule.update", "backup_schedule", schedule.ID.String(), schedule.Name, map[string]any{
		"storage_id":      storage.ID.String(),
		"cron_expression": schedule.CronExpression,
		"backup_type":     schedule.BackupType,
		"enabled":         schedule.Enabled,
	})

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

	backup, err := h.queries.CreateBackup(r.Context(), sqlc.CreateBackupParams{
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
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to trigger backup")
		return
	}

	if err := h.applyVeleroBackupForRow(r.Context(), backup, storage); err != nil && h.log != nil {
		h.log.Warn("failed to apply manual velero Backup CR", "backup_id", backup.ID.String(), "error", err)
	}

	h.publishBackupChanged(backup.ClusterID, backup.ID, "backup")
	recordAudit(r, h.queries, "backup.schedule.trigger", "backup_schedule", schedule.ID.String(), schedule.Name, map[string]any{
		"backup_id":   backup.ID.String(),
		"backup_name": backup.Name,
	})

	w.Header().Set("Location", "/api/v1/backups/"+backup.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, backupToResponse(backup))
}

// --- Restore ---

// CreateRestoreRequest represents the request body for creating a restore operation.
type CreateRestoreRequest struct {
	IncludedNamespaces []string          `json:"included_namespaces"`
	NamespaceMapping   map[string]string `json:"namespace_mapping"`
}

// CreateRestore handles POST /api/v1/backups/{id}/restore/.
func (h *BackupHandler) CreateRestore(w http.ResponseWriter, r *http.Request) {
	backupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid backup ID")
		return
	}

	// Verify the backup exists.
	backup, err := h.queries.GetBackupByID(r.Context(), backupID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Backup not found")
		return
	}
	// A restore overwrites live namespaces on the backup's cluster — the most
	// destructive backup action. Gate it against that cluster's backups grant.
	if !h.authorizeBackup(w, r, backup.ClusterID, rbac.VerbCreate) {
		return
	}

	var req CreateRestoreRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
			return
		}
	}

	veleroNS := backup.VeleroNamespace
	if veleroNS == "" {
		veleroNS = defaultVeleroNamespace
	}

	veleroRestoreName := veleroResourceName("restore", backup.VeleroBackupName)

	includedRaw, _ := json.Marshal(req.IncludedNamespaces)
	mappingRaw, _ := json.Marshal(req.NamespaceMapping)
	if string(mappingRaw) == "null" {
		mappingRaw = json.RawMessage(`{}`)
	}

	params := sqlc.CreateRestoreOperationParams{
		BackupID:           backupID,
		Status:             "pending",
		InitiatedByID:      currentUserUUID(r),
		ClusterID:          backup.ClusterID,
		VeleroNamespace:    veleroNS,
		VeleroRestoreName:  veleroRestoreName,
		IncludedNamespaces: includedRaw,
		NamespaceMapping:   mappingRaw,
	}
	restore, err := h.createRestoreOperation(withOperationIdempotency(r, "restore"), params)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create restore operation")
		return
	}

	h.publishBackupChanged(restore.ClusterID, restore.ID, "restore")
	recordAudit(r, h.queries, "backup.restore.create", "restore_operation", restore.ID.String(), backup.Name, map[string]any{
		"backup_id":           backup.ID.String(),
		"velero_restore_name": veleroRestoreName,
		"included_namespaces": req.IncludedNamespaces,
	})

	w.Header().Set("Location", "/api/v1/backups/restores/"+restore.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, restoreOperationToResponse(restore))
}

// CreateRestoreByBackup is a compatibility alias for CreateRestore.
func (h *BackupHandler) CreateRestoreByBackup(w http.ResponseWriter, r *http.Request) {
	h.CreateRestore(w, r)
}

func (h *BackupHandler) createRestoreOperation(ctx context.Context, params sqlc.CreateRestoreOperationParams) (sqlc.RestoreOperation, error) {
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		if creator, ok := h.queries.(interface {
			CreateRestoreOperationIdempotent(context.Context, sqlc.CreateRestoreOperationIdempotentParams) (sqlc.RestoreOperation, error)
		}); ok {
			return creator.CreateRestoreOperationIdempotent(ctx, sqlc.CreateRestoreOperationIdempotentParams{
				Scope:              idem.scope,
				IdempotencyKey:     idem.key,
				BackupID:           params.BackupID,
				Status:             params.Status,
				InitiatedByID:      params.InitiatedByID,
				ClusterID:          params.ClusterID,
				VeleroNamespace:    params.VeleroNamespace,
				VeleroRestoreName:  params.VeleroRestoreName,
				IncludedNamespaces: params.IncludedNamespaces,
				NamespaceMapping:   params.NamespaceMapping,
			})
		}
	}
	return h.queries.CreateRestoreOperation(ctx, params)
}

// ListRestores handles GET /api/v1/backups/restores/.
func (h *BackupHandler) ListRestores(w http.ResponseWriter, r *http.Request) {
	allow, restricted, ok := h.authz.resourceScopeFilter(w, r, rbac.ResourceBackups, rbac.VerbRead)
	if !ok {
		return
	}
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))

	restores, err := h.queries.ListRestoreOperations(r.Context(), sqlc.ListRestoreOperationsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list restore operations")
		return
	}

	total, err := h.queries.CountRestoreOperations(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count restore operations")
		return
	}

	items := make([]RestoreOperationResponse, 0, len(restores))
	for _, row := range restores {
		if !allow(uuid.UUID(row.ClusterID.Bytes), row.ClusterID.Valid) {
			continue
		}
		items = append(items, restoreOperationToResponse(row))
	}
	if restricted {
		total = int64(len(items))
	}
	RespondPaginated(w, r, items, total)
}

// --- helpers ---

// optionalClusterID parses an optional UUID string into a pgtype.UUID. Empty
// input is permitted and returns an unset value.
func (h *BackupHandler) optionalClusterID(s string) (pgtype.UUID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return pgtype.UUID{}, nil
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}

// encryptCredentials encrypts an aws-style access/secret pair using the
// configured Fernet encryptor. When no encryptor is set it returns "".
func (h *BackupHandler) encryptCredentials(access, secret string) (string, error) {
	if h == nil || h.encryptor == nil {
		return "", nil
	}
	if access == "" && secret == "" {
		return "", nil
	}
	payload, err := json.Marshal(map[string]string{
		"access_key": access,
		"secret_key": secret,
	})
	if err != nil {
		return "", err
	}
	return h.encryptor.Encrypt(string(payload))
}

func legacyBackupCredentialColumns(access, secret, encrypted string) (string, string) {
	if encrypted != "" {
		return "", ""
	}
	return access, secret
}

// decryptCredentials returns the access/secret pair for a storage config,
// preferring the encrypted column when available and falling back to the
// legacy plaintext columns when no encryptor is configured.
func (h *BackupHandler) decryptCredentials(cfg sqlc.BackupStorageConfig) (string, string, error) {
	if h != nil && h.encryptor != nil && cfg.EncryptedCredentials != "" {
		plaintext, err := h.encryptor.Decrypt(cfg.EncryptedCredentials)
		if err != nil {
			return "", "", err
		}
		var creds struct {
			AccessKey string `json:"access_key"`
			SecretKey string `json:"secret_key"`
		}
		if err := json.Unmarshal([]byte(plaintext), &creds); err != nil {
			return "", "", err
		}
		return creds.AccessKey, creds.SecretKey, nil
	}
	return cfg.AccessKey, cfg.SecretKey, nil
}

// storageResponse builds the API representation of a storage config. The
// raw access/secret keys are *never* surfaced; we return only metadata.
func (h *BackupHandler) storageResponse(c sqlc.BackupStorageConfig) map[string]any {
	out := map[string]any{
		"id":               c.ID.String(),
		"name":             c.Name,
		"storage_type":     c.StorageType,
		"bucket":           c.Bucket,
		"prefix":           c.Prefix,
		"region":           c.Region,
		"endpoint_url":     c.EndpointUrl,
		"is_default":       c.IsDefault,
		"velero_namespace": c.VeleroNamespace,
		"bsl_name":         veleroBSLNameFor(c),
		"created_at":       c.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":       c.UpdatedAt.UTC().Format(time.RFC3339),
		"has_credentials":  c.EncryptedCredentials != "" || c.AccessKey != "",
	}
	if c.ClusterID.Valid {
		out["cluster_id"] = uuid.UUID(c.ClusterID.Bytes).String()
	}
	return out
}

// applyVeleroBSL ensures both the credentials Secret and the BSL CR exist on
// the target cluster. No-ops when the storage config has no cluster scope or
// no kubernetes requester is configured.
func (h *BackupHandler) applyVeleroBSL(ctx context.Context, cfg sqlc.BackupStorageConfig, accessKey, secretKey string) error {
	if h == nil || h.requester == nil {
		return nil
	}
	if !cfg.ClusterID.Valid {
		return nil
	}
	clusterID := uuid.UUID(cfg.ClusterID.Bytes).String()
	bslName := veleroBSLNameFor(cfg)
	secretName := veleroSecretNameFor(cfg)
	namespace := cfg.VeleroNamespace
	if namespace == "" {
		namespace = defaultVeleroNamespace
	}

	if accessKey != "" || secretKey != "" {
		secret := renderVeleroCredentialsSecret(secretName, namespace, accessKey, secretKey)
		secretBody, err := json.Marshal(secret)
		if err != nil {
			return err
		}
		createPath, patchPath := veleroSecretPath(namespace, secretName)
		if err := applyJSONBody(ctx, h.requester, clusterID, patchPath, createPath, secretBody); err != nil {
			return fmt.Errorf("apply credentials secret: %w", err)
		}
	}

	bsl := renderVeleroBSL(VeleroBSLRender{
		Name:             bslName,
		Namespace:        namespace,
		Provider:         veleroProviderForStorageType(cfg.StorageType),
		Bucket:           cfg.Bucket,
		Prefix:           cfg.Prefix,
		Region:           cfg.Region,
		S3URL:            cfg.EndpointUrl,
		S3ForcePathStyle: cfg.EndpointUrl != "",
		CredentialSecret: secretName,
		Default:          cfg.IsDefault,
	})
	body, err := json.Marshal(bsl)
	if err != nil {
		return err
	}
	createPath, patchPath := veleroCRDPath(namespace, "backupstoragelocations", bslName)
	return applyJSONBody(ctx, h.requester, clusterID, patchPath, createPath, body)
}

// applyVeleroSchedule projects a BackupSchedule row into a Velero Schedule CR.
func (h *BackupHandler) applyVeleroSchedule(ctx context.Context, sched sqlc.BackupSchedule, storage sqlc.BackupStorageConfig) error {
	if h == nil || h.requester == nil {
		return nil
	}
	if !sched.ClusterID.Valid && !storage.ClusterID.Valid {
		return nil
	}
	clusterPg := sched.ClusterID
	if !clusterPg.Valid {
		clusterPg = storage.ClusterID
	}
	clusterID := uuid.UUID(clusterPg.Bytes).String()
	namespace := sched.VeleroNamespace
	if namespace == "" {
		namespace = defaultVeleroNamespace
	}
	schedName := sched.VeleroScheduleName
	if schedName == "" {
		schedName = veleroResourceName("schedule", sched.Name)
	}
	bslName := veleroBSLNameFor(storage)
	body, err := json.Marshal(renderVeleroSchedule(VeleroScheduleRender{
		Name:               schedName,
		Namespace:          namespace,
		BackupStorageName:  bslName,
		Cron:               sched.CronExpression,
		IncludedNamespaces: veleroNamespacesFromJSON(sched.IncludedNamespaces),
		ExcludedNamespaces: veleroNamespacesFromJSON(sched.ExcludedNamespaces),
		TTL:                sched.Ttl,
		Labels: map[string]string{
			"astronomer.io/schedule-id": sched.ID.String(),
		},
	}))
	if err != nil {
		return err
	}
	createPath, patchPath := veleroCRDPath(namespace, "schedules", schedName)
	return applyJSONBody(ctx, h.requester, clusterID, patchPath, createPath, body)
}

// applyVeleroBackupForRow projects a Backup row into a Velero Backup CR. The
// CR's name comes from the row's velero_backup_name column so subsequent
// status polls can find it.
func (h *BackupHandler) applyVeleroBackupForRow(ctx context.Context, backup sqlc.Backup, storage sqlc.BackupStorageConfig) error {
	if h == nil || h.requester == nil {
		return nil
	}
	clusterPg := backup.ClusterID
	if !clusterPg.Valid {
		clusterPg = storage.ClusterID
	}
	if !clusterPg.Valid {
		return nil
	}
	clusterID := uuid.UUID(clusterPg.Bytes).String()
	namespace := backup.VeleroNamespace
	if namespace == "" {
		namespace = defaultVeleroNamespace
	}
	backupName := backup.VeleroBackupName
	if backupName == "" {
		backupName = veleroResourceName("backup", backup.Name)
	}
	bslName := veleroBSLNameFor(storage)
	body, err := json.Marshal(renderVeleroBackup(VeleroBackupRender{
		Name:               backupName,
		Namespace:          namespace,
		BackupStorageName:  bslName,
		IncludedNamespaces: veleroNamespacesFromJSON(backup.IncludedNamespaces),
		ExcludedNamespaces: veleroNamespacesFromJSON(backup.ExcludedNamespaces),
		Labels: map[string]string{
			"astronomer.io/backup-id": backup.ID.String(),
		},
	}))
	if err != nil {
		return err
	}
	createPath, patchPath := veleroCRDPath(namespace, "backups", backupName)
	if err := applyJSONBody(ctx, h.requester, clusterID, patchPath, createPath, body); err != nil {
		return err
	}
	return h.queries.UpdateBackupVeleroIdentity(ctx, sqlc.UpdateBackupVeleroIdentityParams{
		ID:               backup.ID,
		VeleroBackupName: backupName,
		VeleroNamespace:  namespace,
		ClusterID:        clusterPg,
	})
}

// veleroResourceName produces a DNS-1123-compliant CR name from a kind prefix
// and an arbitrary user-supplied label. We strip non-alphanumerics, lower-case
// and trim to 63 chars to satisfy Kubernetes naming rules.
func veleroResourceName(kind, label string) string {
	parts := []rune{}
	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z':
			parts = append(parts, r)
		case r >= '0' && r <= '9':
			parts = append(parts, r)
		case r == '-' || r == '.':
			parts = append(parts, r)
		case r == ' ' || r == '_' || r == '/' || r == ':':
			parts = append(parts, '-')
		}
	}
	body := strings.Trim(string(parts), "-.")
	if body == "" {
		body = "x"
	}
	out := kind + "-" + body
	if len(out) > 63 {
		out = out[:63]
	}
	return strings.Trim(out, "-.")
}

// buildStorageUpdateParams constructs an UpdateBackupStorageConfigParams that
// preserves every column from the given row. Useful when we only need to
// nudge a single field without re-deriving the entire request body.
func buildStorageUpdateParams(c sqlc.BackupStorageConfig) sqlc.UpdateBackupStorageConfigParams {
	return sqlc.UpdateBackupStorageConfigParams{
		ID:                   c.ID,
		Name:                 c.Name,
		StorageType:          c.StorageType,
		Bucket:               c.Bucket,
		Prefix:               c.Prefix,
		Region:               c.Region,
		EndpointUrl:          c.EndpointUrl,
		AccessKey:            c.AccessKey,
		SecretKey:            c.SecretKey,
		IsDefault:            c.IsDefault,
		ClusterID:            c.ClusterID,
		VeleroNamespace:      c.VeleroNamespace,
		BslName:              c.BslName,
		EncryptedCredentials: c.EncryptedCredentials,
	}
}

// probeS3Bucket issues an authenticated AWS Sig-V4 GET against the bucket's
// list-objects-v2 endpoint with max-keys=1. This is the same probe that
// `velero install` uses to validate a BackupStorageLocation's reachability.
//
// 200 / 204 / 206  → ok.
// 403 with InvalidAccessKeyId / SignatureDoesNotMatch → wrong credentials.
// 404 with NoSuchBucket  → bucket missing.
// network error → wrong endpoint / firewall.
func (h *BackupHandler) probeS3Bucket(ctx context.Context, cfg sqlc.BackupStorageConfig, accessKey, secretKey string) error {
	endpoint := strings.TrimSpace(cfg.EndpointUrl)
	if endpoint == "" {
		// Default to the canonical AWS endpoint for the configured region.
		region := cfg.Region
		if region == "" {
			region = "us-east-1"
		}
		endpoint = fmt.Sprintf("https://s3.%s.amazonaws.com", region)
	}
	host, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint url: %w", err)
	}
	// Path-style addressing (endpoint/bucket/?list-type=2) is what Velero/MinIO
	// use; virtual-host-style works against canonical AWS but breaks against
	// MinIO so we always use path-style.
	host.Path = strings.TrimRight(host.Path, "/") + "/" + cfg.Bucket + "/"
	q := host.Query()
	q.Set("list-type", "2")
	q.Set("max-keys", "1")
	host.RawQuery = q.Encode()

	// SSRF guard: the endpoint URL is operator-supplied (BackupStorageConfig),
	// so refuse to dial a loopback/internal/metadata address. Do not echo the
	// endpoint in the error.
	if err := httpclient.GuardPublicHost(host.String()); err != nil {
		return fmt.Errorf("endpoint is not a permitted public address")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host.String(), nil)
	if err != nil {
		return err
	}

	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	if accessKey != "" && secretKey != "" {
		signAWSV4(req, accessKey, secretKey, region, "s3", time.Now().UTC())
	}

	client := h.httpClient
	if client == nil {
		client = httpclient.DefaultExternal()
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connectivity failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusPartialContent:
		return nil
	case http.StatusForbidden:
		return fmt.Errorf("forbidden (likely invalid credentials): %s", strings.TrimSpace(string(body)))
	case http.StatusNotFound:
		return fmt.Errorf("bucket not found: %s", cfg.Bucket)
	default:
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

// signAWSV4 attaches AWS Signature Version 4 headers to req. This is a minimal
// implementation sufficient for unauthenticated GETs against S3-compatible
// endpoints. It does NOT cover streaming uploads, presigned URLs, or chunked
// transfers — those are out of scope for a connectivity probe.
//
// Reference: https://docs.aws.amazon.com/general/latest/gr/sigv4_signing.html
func signAWSV4(req *http.Request, accessKey, secretKey, region, service string, now time.Time) {
	const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	timeStamp := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	req.Header.Set("X-Amz-Date", timeStamp)
	req.Header.Set("X-Amz-Content-Sha256", emptyPayloadHash)
	req.Header.Set("Host", req.Host)

	// Canonical query string: keys sorted, RFC 3986-encoded.
	values := req.URL.Query()
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	canonicalQuery := ""
	for i, k := range keys {
		if i > 0 {
			canonicalQuery += "&"
		}
		canonicalQuery += awsURIEscape(k) + "=" + awsURIEscape(values.Get(k))
	}

	// Canonical headers (host, x-amz-content-sha256, x-amz-date).
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n",
		req.Host, emptyPayloadHash, timeStamp)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"

	canonicalRequest := strings.Join([]string{
		req.Method,
		awsURIEscapePath(req.URL.Path),
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		emptyPayloadHash,
	}, "\n")

	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		timeStamp,
		credentialScope,
		hashSHA256(canonicalRequest),
	}, "\n")

	dateKey := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	regionKey := hmacSHA256(dateKey, region)
	serviceKey := hmacSHA256(regionKey, service)
	signingKey := hmacSHA256(serviceKey, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature,
	))
}

func hashSHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(value))
	return h.Sum(nil)
}

// awsURIEscape encodes a string for use in an AWS Sig-V4 canonical query
// string. AWS requires unreserved characters per RFC 3986 only; everything
// else is percent-encoded with upper-case hex.
func awsURIEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'),
			c == '-' || c == '_' || c == '.' || c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// awsURIEscapePath escapes a URL path segment-by-segment, leaving '/' literal.
func awsURIEscapePath(p string) string {
	if p == "" {
		return "/"
	}
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = awsURIEscape(part)
	}
	return strings.Join(parts, "/")
}
