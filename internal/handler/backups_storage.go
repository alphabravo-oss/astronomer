package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/redaction"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// --- Storage Configs ---

// ListStorageConfigs handles GET /api/v1/backups/storage/.
func (h *BackupHandler) ListStorageConfigs(w http.ResponseWriter, r *http.Request) {
	allow, restricted, ok := h.authz.resourceScopeFilter(w, r, rbac.ResourceBackups, rbac.VerbRead)
	if !ok {
		return
	}
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

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
	paging.Write(w, out, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(out)))
}

// CreateStorageConfigRequest represents the request body for creating a storage config.
// openapi:request BackupStorageConfigRequest
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
	if bslName == "" {
		bslName = veleroBSLNameFor(sqlc.BackupStorageConfig{Name: req.Name})
	}

	params := sqlc.CreateBackupStorageConfigParams{
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
	}
	config, err := executeMutation(r, h.runTx,
		func(q BackupMutationTx) (sqlc.BackupStorageConfig, error) {
			return q.CreateBackupStorageConfig(r.Context(), params)
		},
		func(config sqlc.BackupStorageConfig) mutationAuditEvent {
			return mutationAuditEvent{
				action: "backup.storage.create", resourceType: "backup_storage_config", resourceID: config.ID.String(), resourceName: config.Name,
				status: http.StatusCreated, detail: map[string]any{
					"storage_type": config.StorageType, "bucket": config.Bucket, "region": config.Region, "is_default": config.IsDefault,
				},
			}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create storage config")
		return
	}
	if err := h.applyVeleroBSL(r.Context(), config, req.AccessKey, req.SecretKey); err != nil && h.log != nil {
		h.log.Warn("failed to apply velero BSL", "config_id", config.ID.String(), "error", err)
	}

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
	_, err = executeMutation(r, h.runTx,
		func(q BackupMutationTx) (sqlc.BackupStorageConfig, error) {
			return existing, q.DeleteBackupStorageConfig(r.Context(), id)
		},
		func(config sqlc.BackupStorageConfig) mutationAuditEvent {
			return mutationAuditEvent{action: "backup.storage.delete", resourceType: "backup_storage_config", resourceID: config.ID.String(), resourceName: configName, status: http.StatusNoContent}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete storage config")
		return
	}

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
	if bslName == "" {
		bslName = veleroBSLNameFor(sqlc.BackupStorageConfig{Name: req.Name})
	}

	params := sqlc.UpdateBackupStorageConfigParams{
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
	}
	config, err := executeMutation(r, h.runTx,
		func(q BackupMutationTx) (sqlc.BackupStorageConfig, error) {
			return q.UpdateBackupStorageConfig(r.Context(), params)
		},
		func(config sqlc.BackupStorageConfig) mutationAuditEvent {
			return mutationAuditEvent{
				action: "backup.storage.update", resourceType: "backup_storage_config", resourceID: config.ID.String(), resourceName: config.Name,
				status: http.StatusOK, detail: map[string]any{
					"storage_type": config.StorageType, "bucket": config.Bucket, "region": config.Region, "is_default": config.IsDefault,
				},
			}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update storage config")
		return
	}

	if err := h.applyVeleroBSL(r.Context(), config, req.AccessKey, req.SecretKey); err != nil && h.log != nil {
		h.log.Warn("failed to apply velero BSL", "config_id", config.ID.String(), "error", err)
	}

	RespondJSON(w, http.StatusOK, h.storageResponse(config))
}

// TestStorageConfig handles POST /api/v1/backups/storage/{id}/test-connection/.
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
		safeReason := redaction.String(err.Error())
		if auditErr := h.persistStorageTestAudit(r, cfg, false, safeReason); auditErr != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable, "Mandatory audit storage is unavailable")
			return
		}
		RespondJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": safeReason,
		})
		return
	}
	if err := h.persistStorageTestAudit(r, cfg, true, ""); err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable, "Mandatory audit storage is unavailable")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Backup storage configuration is reachable and credentials are valid",
	})
}

func (h *BackupHandler) persistStorageTestAudit(r *http.Request, cfg sqlc.BackupStorageConfig, success bool, reason string) error {
	detail := map[string]any{"success": success}
	if reason != "" {
		detail["reason"] = reason
	}
	if h == nil {
		return audit.ErrMandatoryPersistenceUnavailable
	}
	return recordMandatoryAudit(r, h.queries, "backup.storage.test", "backup_storage_config", cfg.ID.String(), cfg.Name, detail)
}

// --- Backups ---
