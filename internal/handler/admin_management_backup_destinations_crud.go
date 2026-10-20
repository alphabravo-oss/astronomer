package handler

import (
	"errors"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/pkg/crypto"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

func (h *AdminDrillHandler) GetDestination(w http.ResponseWriter, r *http.Request) {
	if !h.gateAction(w, r, "admin.management_backup.destination.viewed") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidID, "Invalid destination ID")
		return
	}
	row, err := h.queries.GetManagementBackupDestination(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Destination not found")
		return
	}
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.DBError, "Failed to load destination")
		return
	}
	RespondJSON(w, http.StatusOK, destinationView(row))
}

// CreateDestination handles POST /admin/management-backup/destinations/.
func (h *AdminDrillHandler) CreateDestination(w http.ResponseWriter, r *http.Request) {
	if !h.gateManagementBackupMutation(w, r) {
		return
	}
	var req ManagementBackupDestinationWrite
	if !decodeAndValidate(w, r, &req) {
		return
	}
	req.normalize()
	if req.AccessKey == "" || req.SecretKey == "" {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Access key and secret key are required")
		return
	}
	encrypted, err := h.encryptDestinationCredentials(req.AccessKey, req.SecretKey)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.CryptoError, "Failed to encrypt credentials")
		return
	}
	createdBy := pgtype.UUID{}
	if user, ok := reqctx.AuthenticatedUser(r.Context()); ok && user != nil {
		if id, err := uuid.Parse(user.ID); err == nil {
			createdBy = pgtype.UUID{Bytes: id, Valid: true}
		}
	}
	if h.runTx == nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.RunnerUnwired, "management backup transaction runner is not configured")
		return
	}
	var row sqlc.ManagementBackupDestination
	err = h.runTx(r.Context(), func(q ManagementBackupMutationTx) error {
		var createErr error
		row, createErr = q.CreateManagementBackupDestination(r.Context(), sqlc.CreateManagementBackupDestinationParams{
			Name:             req.Name,
			Bucket:           req.Bucket,
			Prefix:           req.Prefix,
			Region:           req.Region,
			EndpointUrl:      req.EndpointURL,
			CredentialsKeyID: encrypted.KeyID, CredentialsAlgorithm: encrypted.Algorithm, CredentialsCiphertext: encrypted.Ciphertext, CredentialsNonce: encrypted.Nonce,
			Schedule:    req.Schedule,
			Enabled:     derefBool(req.Enabled, true),
			KeepDaily:   derefInt32(req.KeepDaily, 30),
			KeepWeekly:  derefInt32(req.KeepWeekly, 12),
			KeepMonthly: derefInt32(req.KeepMonthly, 6),
			CreatedByID: createdBy,
		})
		if createErr != nil {
			return createErr
		}
		if err := enqueueManagementBackupReconcile(r.Context(), q, row); err != nil {
			return err
		}
		return recordAuditOutbox(r, q, "admin.management_backup.destination.created", "management_backup_destination", row.ID.String(), row.Name, http.StatusCreated, managementBackupAuditDetail(row))
	})
	if err != nil {
		h.respondDestinationWriteError(w, r, err)
		return
	}
	RespondJSON(w, http.StatusCreated, destinationView(row))
}

// UpdateDestination handles PUT /admin/management-backup/destinations/{id}/.
func (h *AdminDrillHandler) UpdateDestination(w http.ResponseWriter, r *http.Request) {
	if !h.gateManagementBackupMutation(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidID, "Invalid destination ID")
		return
	}
	var req ManagementBackupDestinationWrite
	if !decodeAndValidate(w, r, &req) {
		return
	}
	req.normalize()
	if h.runTx == nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.RunnerUnwired, "management backup transaction runner is not configured")
		return
	}
	var row sqlc.ManagementBackupDestination
	err = h.runTx(r.Context(), func(q ManagementBackupMutationTx) error {
		existing, loadErr := q.GetManagementBackupDestinationForUpdate(r.Context(), id)
		if loadErr != nil {
			return loadErr
		}
		if existing.DesiredState == "deleted" {
			return pgx.ErrNoRows
		}
		if existing.ReconcileStatus == "applying" {
			return errManagementBackupReconcileActive
		}
		encrypted := crypto.FromParts(existing.CredentialsKeyID, existing.CredentialsAlgorithm, existing.CredentialsCiphertext, existing.CredentialsNonce)
		access, secret, decErr := h.decryptDestinationCredentials(existing)
		if decErr != nil {
			return decErr
		}
		if req.AccessKey != "" && req.AccessKey != PasswordSentinelEncrypted {
			access = req.AccessKey
		}
		if req.SecretKey != "" && req.SecretKey != PasswordSentinelEncrypted {
			secret = req.SecretKey
		}
		if access != "" && secret != "" {
			encrypted, decErr = h.encryptDestinationCredentials(access, secret)
			if decErr != nil {
				return decErr
			}
		}
		row, loadErr = q.UpdateManagementBackupDestination(r.Context(), sqlc.UpdateManagementBackupDestinationParams{
			ID:               id,
			Name:             req.Name,
			Bucket:           req.Bucket,
			Prefix:           req.Prefix,
			Region:           req.Region,
			EndpointUrl:      req.EndpointURL,
			CredentialsKeyID: encrypted.KeyID, CredentialsAlgorithm: encrypted.Algorithm, CredentialsCiphertext: encrypted.Ciphertext, CredentialsNonce: encrypted.Nonce,
			Schedule: req.Schedule,
			Enabled:  derefBool(req.Enabled, existing.Enabled), KeepDaily: derefInt32(req.KeepDaily, existing.KeepDaily), KeepWeekly: derefInt32(req.KeepWeekly, existing.KeepWeekly), KeepMonthly: derefInt32(req.KeepMonthly, existing.KeepMonthly),
		})
		if loadErr != nil {
			return loadErr
		}
		if err := enqueueManagementBackupReconcile(r.Context(), q, row); err != nil {
			return err
		}
		return recordAuditOutbox(r, q, "admin.management_backup.destination.updated", "management_backup_destination", row.ID.String(), row.Name, http.StatusOK, managementBackupAuditDetail(row))
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Destination not found")
			return
		}
		h.respondDestinationWriteError(w, r, err)
		return
	}
	RespondJSON(w, http.StatusOK, destinationView(row))
}

// DeleteDestination handles DELETE /admin/management-backup/destinations/{id}/.
func (h *AdminDrillHandler) DeleteDestination(w http.ResponseWriter, r *http.Request) {
	if !h.gateManagementBackupMutation(w, r) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidID, "Invalid destination ID")
		return
	}
	if h.runTx == nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.RunnerUnwired, "management backup transaction runner is not configured")
		return
	}
	r = r.WithContext(withOperationIdempotency(r, "admin_management_backup_destination_delete"))
	digest, err := canonicalOperationRequestDigest(struct {
		Action        string `json:"action"`
		DestinationID string `json:"destination_id"`
	}{Action: "delete", DestinationID: id.String()})
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.EncodeError, "Failed to encode destination deletion request")
		return
	}
	var deleted sqlc.ManagementBackupDestination
	var receipt ManagementBackupDestinationDeleteReceipt
	err = h.runTx(r.Context(), func(q ManagementBackupMutationTx) error {
		idemQ, ok := q.(resourceOperationIdempotencyQuerier)
		if !ok {
			return errors.New("management backup deletion idempotency store is not configured")
		}
		_, stored, replay, claimErr := claimOperationReceipt[ManagementBackupDestinationDeleteReceipt](r.Context(), idemQ, "management_backup_destination_deletes", digest)
		if claimErr != nil {
			return claimErr
		}
		if replay {
			receipt = stored
			return nil
		}
		existing, err := q.GetManagementBackupDestinationForUpdate(r.Context(), id)
		if err != nil {
			return err
		}
		if existing.DesiredState == "deleted" {
			deleted = existing
			receipt = managementBackupDeleteReceipt(id, deleted)
			return attachOperationReceipt(r.Context(), idemQ, "management_backup_destination_deletes", id, digest, receipt)
		}
		if existing.ReconcileStatus == "applying" {
			return errManagementBackupReconcileActive
		}
		row, err := q.MarkManagementBackupDestinationDeleted(r.Context(), id)
		if err != nil {
			return err
		}
		deleted = row
		receipt = managementBackupDeleteReceipt(id, deleted)
		if err = enqueueManagementBackupReconcile(r.Context(), q, row); err != nil {
			return err
		}
		if auditErr := recordAuditOutbox(r, q, "admin.management_backup.destination.delete_accepted", "management_backup_destination", id.String(), existing.Name, http.StatusAccepted, managementBackupAuditDetail(row)); auditErr != nil {
			return auditErr
		}
		return attachOperationReceipt(r.Context(), idemQ, "management_backup_destination_deletes", id, digest, receipt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Destination not found")
		return
	}
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			apiutil.WriteError(w, r, http.StatusConflict, apiutil.Conflict, "Idempotency-Key already identifies a different management backup destination deletion")
			return
		}
		if errors.Is(err, errManagementBackupReconcileActive) {
			apiutil.WriteError(w, r, http.StatusConflict, apiutil.Conflict, "Destination reconciliation is active; retry after the current generation settles")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apiutil.DBError, "Failed to persist destination deletion")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/admin/management-backup/destinations/"+receipt.DestinationID+"/", receipt)
}
