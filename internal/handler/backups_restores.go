package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// CreateRestoreRequest represents the request body for creating a restore operation.
// openapi:request BackupRestoreRequest
type CreateRestoreRequest struct {
	IncludedNamespaces []string          `json:"included_namespaces"`
	NamespaceMapping   map[string]string `json:"namespace_mapping"`
}

// CreateRestore handles POST /api/v1/backups/{id}/restore/.
func (h *BackupHandler) CreateRestoreByBackup(w http.ResponseWriter, r *http.Request) {
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
	ctx := withOperationIdempotency(r, "restore")
	restore, err := executeMutation(r, h.runTx,
		func(q BackupMutationTx) (sqlc.RestoreOperation, error) {
			return createRestoreOperationWith(ctx, q, params)
		},
		func(restore sqlc.RestoreOperation) mutationAuditEvent {
			return mutationAuditEvent{
				action: "backup.restore.create", resourceType: "restore_operation", resourceID: restore.ID.String(), resourceName: backup.Name,
				status: http.StatusCreated, detail: map[string]any{
					"backup_id": backup.ID.String(), "velero_restore_name": veleroRestoreName,
					"included_namespaces": req.IncludedNamespaces,
				},
			}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create restore operation")
		return
	}

	h.publishBackupChanged(restore.ClusterID, restore.ID, "restore")

	w.Header().Set("Location", "/api/v1/backups/restores/"+restore.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, restoreOperationToResponse(restore))
}

func (h *BackupHandler) createRestoreOperation(ctx context.Context, params sqlc.CreateRestoreOperationParams) (sqlc.RestoreOperation, error) {
	return createRestoreOperationWith(ctx, h.queries, params)
}

func createRestoreOperationWith(ctx context.Context, q any, params sqlc.CreateRestoreOperationParams) (sqlc.RestoreOperation, error) {
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		if creator, ok := q.(interface {
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
	creator, ok := q.(interface {
		CreateRestoreOperation(context.Context, sqlc.CreateRestoreOperationParams) (sqlc.RestoreOperation, error)
	})
	if !ok {
		return sqlc.RestoreOperation{}, fmt.Errorf("restore persistence is unavailable")
	}
	return creator.CreateRestoreOperation(ctx, params)
}

// ListRestores handles GET /api/v1/backups/restores/.
func (h *BackupHandler) ListRestores(w http.ResponseWriter, r *http.Request) {
	allow, restricted, ok := h.authz.resourceScopeFilter(w, r, rbac.ResourceBackups, rbac.VerbRead)
	if !ok {
		return
	}
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

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
	paging.Write(w, items, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(items)))
}

// GetRestore handles GET /api/v1/backups/restores/{id}/. Restore creation has
// always returned this URL in Location; keeping an object endpoint here makes
// that header dereferenceable and lets clients poll one operation without
// repeatedly scanning a fleet-wide list.
func (h *BackupHandler) GetRestore(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid restore ID")
		return
	}

	restore, err := h.queries.GetRestoreOperationByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Restore operation not found")
		return
	}
	if !h.authorizeBackup(w, r, restore.ClusterID, rbac.VerbRead) {
		return
	}

	RespondJSON(w, http.StatusOK, restoreOperationToResponse(restore))
}

// --- helpers ---
