package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type ManagementBackupDestinationDeleteReceipt struct {
	DestinationID string `json:"destination_id"`
	Generation    int64  `json:"generation"`
	DesiredState  string `json:"desired_state"`
	Status        string `json:"status"`
}

func managementBackupDeleteReceipt(id uuid.UUID, row sqlc.ManagementBackupDestination) ManagementBackupDestinationDeleteReceipt {
	return ManagementBackupDestinationDeleteReceipt{
		DestinationID: id.String(), Generation: row.DesiredGeneration,
		DesiredState: row.DesiredState, Status: row.ReconcileStatus,
	}
}

// TestDestination handles POST /admin/management-backup/destinations/{id}/test/.
func (h *AdminDrillHandler) TestDestination(w http.ResponseWriter, r *http.Request) {
	if !h.gateManagementBackupMutation(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid destination ID")
		return
	}
	h.createManagementBackupOperation(w, r, id, "management_backup_test")
}

// RunDestination handles POST /admin/management-backup/destinations/{id}/run/.
func (h *AdminDrillHandler) RunDestination(w http.ResponseWriter, r *http.Request) {
	if !h.gateManagementBackupMutation(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid destination ID")
		return
	}
	h.createManagementBackupOperation(w, r, id, "management_backup_run")
}

func (h *AdminDrillHandler) GetManagementBackupOperation(w http.ResponseWriter, r *http.Request) {
	if !h.gateAction(w, r, "admin.management_backup.operation.viewed") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	q, ok := h.queries.(managementBackupWorkerQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.DBError, "Management backup operation store is unavailable")
		return
	}
	op, err := q.GetWorkloadOperation(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (op.TargetType != "management_backup_destination" || !strings.HasPrefix(op.OperationType, "management_backup_"))) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Management backup operation not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to load management backup operation")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"id": op.ID.String(), "destination_id": op.TargetKey, "operation_type": op.OperationType, "status": op.Status, "attempt_count": op.AttemptCount, "error_message": op.ErrorMessage, "created_at": op.CreatedAt, "updated_at": op.UpdatedAt})
}

func enqueueManagementBackupReconcile(ctx context.Context, q ManagementBackupMutationTx, row sqlc.ManagementBackupDestination) error {
	task, err := tasks.NewManagementBackupReconcileTask(row.ID, row.DesiredGeneration)
	if err != nil {
		return err
	}
	_, err = tasks.EnqueueTaskOutbox(ctx, q, task, tasks.TaskOutboxOptions{DedupeKey: fmt.Sprintf("management_backup:%s:%d", row.ID, row.DesiredGeneration), MaxRetry: 8, Timeout: 5 * time.Minute})
	return err
}

func managementBackupAuditDetail(row sqlc.ManagementBackupDestination) map[string]any {
	return map[string]any{"destination_id": row.ID.String(), "generation": row.DesiredGeneration, "desired_state": row.DesiredState}
}

func (h *AdminDrillHandler) createManagementBackupOperation(w http.ResponseWriter, r *http.Request, id uuid.UUID, operationType string) {
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "management backup transaction runner is not configured")
		return
	}
	idem, ok := operationIdempotencyFromContext(withOperationIdempotency(r, "management_backup"))
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Idempotency-Key is required")
		return
	}
	var operation sqlc.WorkloadOperation
	err := h.runTx(r.Context(), func(q ManagementBackupMutationTx) error {
		row, err := q.GetManagementBackupDestinationForUpdate(r.Context(), id)
		if err != nil || row.DesiredState == "deleted" {
			return pgx.ErrNoRows
		}
		operation, err = q.CreateWorkloadOperationIdempotent(r.Context(), sqlc.CreateWorkloadOperationIdempotentParams{Scope: idem.scope, IdempotencyKey: idem.key, TargetType: "management_backup_destination", TargetKey: id.String(), OperationType: operationType, Payload: json.RawMessage(`{}`), Status: "pending", CreatedByID: currentUserUUID(r)})
		if err != nil {
			return err
		}
		if operation.TargetKey != id.String() || operation.OperationType != operationType {
			return errWorkloadOperationIdempotencyConflict
		}
		task, err := tasks.NewManagementBackupOperationTask(operation.ID)
		if err != nil {
			return err
		}
		if _, err = tasks.EnqueueTaskOutbox(r.Context(), q, task, tasks.TaskOutboxOptions{DedupeKey: "management_backup_operation:" + operation.ID.String(), MaxRetry: 20, Timeout: 5 * time.Minute, MaxDeliveryAttempts: 30}); err != nil {
			return err
		}
		return recordAuditOutbox(r, q, "admin."+operationType+".accepted", "workload_operation", operation.ID.String(), operationType, http.StatusAccepted, map[string]any{"operation_id": operation.ID.String(), "destination_id": id.String()})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Destination not found")
		return
	}
	if errors.Is(err, errWorkloadOperationIdempotencyConflict) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different management backup operation")
		return
	}
	var activeRunErr *pgconn.PgError
	if errors.As(err, &activeRunErr) && activeRunErr.Code == "23505" && activeRunErr.ConstraintName == "workload_operations_active_management_backup_run_idx" {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Another backup run is already active for this destination")
		return
	}
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to persist management backup operation")
		return
	}
	operationURL := "/api/v1/admin/management-backup/operations/" + operation.ID.String() + "/"
	w.Header().Set("Location", operationURL)
	w.Header().Set("Retry-After", "2")
	RespondJSON(w, http.StatusAccepted, map[string]any{"id": operation.ID.String(), "status": operation.Status, "operation_type": operation.OperationType, "destination_id": id.String(), "operation_url": operationURL})
}
