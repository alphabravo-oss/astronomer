package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

const defaultManagementBackupImage = "ghcr.io/alphabravocompany/pgdump-s3:16-awscli"

var (
	errManagementBackupReconcileActive = errors.New("management backup reconciliation is active")
	errManagementBackupStaleGeneration = errors.New("management backup generation is stale")
)

const managementBackupDumpScript = `set -eu
STAMP="$(date -u +%Y-%m-%dT%H-%M-%SZ)"
DOW="$(date -u +%u)"
DOM="$(date -u +%d)"
BUCKET="${MANAGEMENT_BACKUP_BUCKET}"
PREFIX="${MANAGEMENT_BACKUP_PREFIX:-astronomer-pg}"
RELEASE="${MANAGEMENT_BACKUP_RELEASE}"
DAILY_KEY="${PREFIX}/${RELEASE}/daily/${STAMP}.pgcustom"
WEEKLY_KEY="${PREFIX}/${RELEASE}/weekly/${STAMP}.pgcustom"
MONTHLY_KEY="${PREFIX}/${RELEASE}/monthly/${STAMP}.pgcustom"
DUMP_PATH="/tmp/${STAMP}.pgcustom"
export AWS_SHARED_CREDENTIALS_FILE="/var/run/aws/credentials"
export AWS_DEFAULT_REGION="${MANAGEMENT_BACKUP_REGION}"
AWS_S3_FLAGS=""
if [ -n "${MANAGEMENT_BACKUP_ENDPOINT:-}" ]; then
  AWS_S3_FLAGS="--endpoint-url ${MANAGEMENT_BACKUP_ENDPOINT}"
fi
echo "Starting pg_dump at ${STAMP} (release=${RELEASE} dest=${MANAGEMENT_BACKUP_DEST_NAME})"
pg_dump --dbname="${DATABASE_URL}" --format=custom --no-owner --no-acl --file="${DUMP_PATH}"
DUMP_SIZE="$(wc -c < "${DUMP_PATH}")"
echo "pg_dump complete: ${DUMP_SIZE} bytes"
echo "Uploading daily: s3://${BUCKET}/${DAILY_KEY}"
aws s3 cp ${AWS_S3_FLAGS} "${DUMP_PATH}" "s3://${BUCKET}/${DAILY_KEY}"
if [ "${DOW}" = "7" ]; then
  echo "Promoting to weekly: s3://${BUCKET}/${WEEKLY_KEY}"
  aws s3 cp ${AWS_S3_FLAGS} "${DUMP_PATH}" "s3://${BUCKET}/${WEEKLY_KEY}"
fi
if [ "${DOM}" = "01" ]; then
  echo "Promoting to monthly: s3://${BUCKET}/${MONTHLY_KEY}"
  aws s3 cp ${AWS_S3_FLAGS} "${DUMP_PATH}" "s3://${BUCKET}/${MONTHLY_KEY}"
fi
rm -f "${DUMP_PATH}"
prune_tier() {
  TIER="$1"
  KEEP="$2"
  PFX="${PREFIX}/${RELEASE}/${TIER}/"
  echo "Pruning ${TIER} retention (keep ${KEEP})"
  KEYS="$(aws s3api list-objects-v2 ${AWS_S3_FLAGS} --bucket "${BUCKET}" --prefix "${PFX}" --query 'Contents[].Key' --output text 2>/dev/null || echo '')"
  if [ -z "${KEYS}" ] || [ "${KEYS}" = "None" ]; then
    echo "  (no objects under ${PFX})"
    return 0
  fi
  echo "${KEYS}" | tr '\t' '\n' | tr ' ' '\n' | sed '/^$/d' | sort -r | tail -n +"$((KEEP + 1))" | while read -r OLD; do
    [ -z "${OLD}" ] && continue
    echo "  rm s3://${BUCKET}/${OLD}"
    aws s3 rm ${AWS_S3_FLAGS} "s3://${BUCKET}/${OLD}" || true
  done
}
prune_tier "daily"   "${MANAGEMENT_BACKUP_KEEP_DAILY}"
prune_tier "weekly"  "${MANAGEMENT_BACKUP_KEEP_WEEKLY}"
prune_tier "monthly" "${MANAGEMENT_BACKUP_KEEP_MONTHLY}"
echo "Backup OK."
`

// ManagementBackupDestinationWrite is the create/update body.
// openapi:request ManagementBackupDestinationWriteRequest
type ManagementBackupDestinationWrite struct {
	Name        string `json:"name" validate:"required,max=255"`
	Bucket      string `json:"bucket" validate:"required,max=255"`
	Prefix      string `json:"prefix"`
	Region      string `json:"region"`
	EndpointURL string `json:"endpoint_url"`
	AccessKey   string `json:"access_key"`
	SecretKey   string `json:"secret_key"`
	Schedule    string `json:"schedule"`
	Enabled     *bool  `json:"enabled"`
	KeepDaily   *int32 `json:"keep_daily"`
	KeepWeekly  *int32 `json:"keep_weekly"`
	KeepMonthly *int32 `json:"keep_monthly"`
}

// GetDestination returns the exact durable destination state referenced by
// deletion receipts. Credentials remain redacted by destinationView.
func (h *AdminDrillHandler) GetDestination(w http.ResponseWriter, r *http.Request) {
	if !h.gateAction(w, r, "admin.management_backup.destination.viewed") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid destination ID")
		return
	}
	row, err := h.queries.GetManagementBackupDestination(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Destination not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to load destination")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Access key and secret key are required")
		return
	}
	encrypted, err := h.encryptDestinationCredentials(req.AccessKey, req.SecretKey)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CryptoError, "Failed to encrypt credentials")
		return
	}
	createdBy := pgtype.UUID{}
	if user, ok := middleware.GetAuthenticatedUser(r.Context()); ok && user != nil {
		if id, err := uuid.Parse(user.ID); err == nil {
			createdBy = pgtype.UUID{Bytes: id, Valid: true}
		}
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "management backup transaction runner is not configured")
		return
	}
	var row sqlc.ManagementBackupDestination
	err = h.runTx(r.Context(), func(q ManagementBackupMutationTx) error {
		var createErr error
		row, createErr = q.CreateManagementBackupDestination(r.Context(), sqlc.CreateManagementBackupDestinationParams{
			Name:                 req.Name,
			Bucket:               req.Bucket,
			Prefix:               req.Prefix,
			Region:               req.Region,
			EndpointUrl:          req.EndpointURL,
			EncryptedCredentials: encrypted,
			Schedule:             req.Schedule,
			Enabled:              derefBool(req.Enabled, true),
			KeepDaily:            derefInt32(req.KeepDaily, 30),
			KeepWeekly:           derefInt32(req.KeepWeekly, 12),
			KeepMonthly:          derefInt32(req.KeepMonthly, 6),
			CreatedByID:          createdBy,
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid destination ID")
		return
	}
	var req ManagementBackupDestinationWrite
	if !decodeAndValidate(w, r, &req) {
		return
	}
	req.normalize()
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "management backup transaction runner is not configured")
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
		encrypted := existing.EncryptedCredentials
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
			ID:                   id,
			Name:                 req.Name,
			Bucket:               req.Bucket,
			Prefix:               req.Prefix,
			Region:               req.Region,
			EndpointUrl:          req.EndpointURL,
			EncryptedCredentials: encrypted,
			Schedule:             req.Schedule,
			Enabled:              derefBool(req.Enabled, existing.Enabled), KeepDaily: derefInt32(req.KeepDaily, existing.KeepDaily), KeepWeekly: derefInt32(req.KeepWeekly, existing.KeepWeekly), KeepMonthly: derefInt32(req.KeepMonthly, existing.KeepMonthly),
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
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Destination not found")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid destination ID")
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "management backup transaction runner is not configured")
		return
	}
	r = r.WithContext(withOperationIdempotency(r, "admin_management_backup_destination_delete"))
	digest, err := canonicalOperationRequestDigest(struct {
		Action        string `json:"action"`
		DestinationID string `json:"destination_id"`
	}{Action: "delete", DestinationID: id.String()})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode destination deletion request")
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
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Destination not found")
		return
	}
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different management backup destination deletion")
			return
		}
		if errors.Is(err, errManagementBackupReconcileActive) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Destination reconciliation is active; retry after the current generation settles")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to persist destination deletion")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/admin/management-backup/destinations/"+receipt.DestinationID+"/", receipt)
}

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

func (req *ManagementBackupDestinationWrite) normalize() {
	req.Name = strings.TrimSpace(req.Name)
	req.Bucket = strings.TrimSpace(req.Bucket)
	req.Prefix = strings.TrimSpace(req.Prefix)
	if req.Prefix == "" {
		req.Prefix = "astronomer-pg"
	}
	req.Region = strings.TrimSpace(req.Region)
	if req.Region == "" {
		req.Region = "us-east-1"
	}
	req.EndpointURL = strings.TrimSpace(req.EndpointURL)
	req.Schedule = strings.TrimSpace(req.Schedule)
	if req.Schedule == "" {
		req.Schedule = "0 3 * * *"
	}
}

func derefBool(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func derefInt32(v *int32, fallback int32) int32 {
	if v == nil {
		return fallback
	}
	return *v
}

func (h *AdminDrillHandler) respondDestinationWriteError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errManagementBackupReconcileActive) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Destination reconciliation is active; retry after the current generation settles")
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A destination with that name already exists")
		return
	}
	RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to persist management backup destination")
}

func (h *AdminDrillHandler) encryptDestinationCredentials(access, secret string) (string, error) {
	if h == nil || h.encryptor == nil {
		return "", errors.New("management backup credential encryption is not configured")
	}
	payload, err := json.Marshal(map[string]string{"access_key": access, "secret_key": secret})
	if err != nil {
		return "", err
	}
	return h.encryptor.Encrypt(string(payload))
}

func (h *AdminDrillHandler) decryptDestinationCredentials(row sqlc.ManagementBackupDestination) (string, string, error) {
	if h == nil || h.encryptor == nil || row.EncryptedCredentials == "" {
		return "", "", errors.New("management backup credentials are unavailable")
	}
	plaintext, err := h.encryptor.Decrypt(row.EncryptedCredentials)
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

func (h *AdminDrillHandler) reconcileDestination(ctx context.Context, row sqlc.ManagementBackupDestination, access, secret string, fence func() error) error {
	if h.k8s == nil || h.namespace == "" {
		return nil
	}
	if !row.Enabled {
		return h.deleteDestinationResources(ctx, row.ID, row.DesiredGeneration, fence)
	}
	if err := fence(); err != nil {
		return err
	}
	if err := h.upsertDestinationSecret(ctx, row.ID, row.DesiredGeneration, access, secret, fence); err != nil {
		return err
	}
	if err := fence(); err != nil {
		return err
	}
	return h.upsertDestinationCronJob(ctx, row, fence)
}

func (h *AdminDrillHandler) deleteDestinationResources(ctx context.Context, id uuid.UUID, generation int64, fence func() error) error {
	if h.k8s == nil || h.namespace == "" {
		return nil
	}
	name := h.destinationResourceName(id)
	cronJobs := h.k8s.BatchV1().CronJobs(h.namespace)
	cronJob, err := cronJobs.Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		if managementBackupObjectGeneration(cronJob.Annotations) > generation {
			return errManagementBackupStaleGeneration
		}
		if err = fence(); err != nil {
			return err
		}
		rv := cronJob.ResourceVersion
		if err = cronJobs.Delete(ctx, name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{ResourceVersion: &rv}}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	secrets := h.k8s.CoreV1().Secrets(h.namespace)
	secret, err := secrets.Get(ctx, name+"-aws", metav1.GetOptions{})
	if err == nil {
		if managementBackupObjectGeneration(secret.Annotations) > generation {
			return errManagementBackupStaleGeneration
		}
		if err = fence(); err != nil {
			return err
		}
		rv := secret.ResourceVersion
		if err = secrets.Delete(ctx, name+"-aws", metav1.DeleteOptions{Preconditions: &metav1.Preconditions{ResourceVersion: &rv}}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (h *AdminDrillHandler) upsertDestinationSecret(ctx context.Context, id uuid.UUID, generation int64, access, secret string, fence func() error) error {
	name := h.destinationResourceName(id) + "-aws"
	body := fmt.Sprintf("[default]\naws_access_key_id = %s\naws_secret_access_key = %s\n", access, secret)
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: h.namespace,
			Labels:    h.destinationLabels(id),
			Annotations: map[string]string{
				"astronomer.io/management-backup-generation": strconv.FormatInt(generation, 10),
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"credentials": []byte(body)},
	}
	if existing, err := h.k8s.CoreV1().Secrets(h.namespace).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		if err = fence(); err != nil {
			return err
		}
		_, err = h.k8s.CoreV1().Secrets(h.namespace).Create(ctx, sec, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return err
	} else {
		if managementBackupObjectGeneration(existing.Annotations) > generation {
			return errManagementBackupStaleGeneration
		}
		sec.ResourceVersion = existing.ResourceVersion
	}
	if err := fence(); err != nil {
		return err
	}
	_, err := h.k8s.CoreV1().Secrets(h.namespace).Update(ctx, sec, metav1.UpdateOptions{})
	return err
}

func (h *AdminDrillHandler) upsertDestinationCronJob(ctx context.Context, row sqlc.ManagementBackupDestination, fence func() error) error {
	name := h.destinationResourceName(row.ID)
	cj := h.buildDestinationCronJob(ctx, row)
	if existing, err := h.k8s.BatchV1().CronJobs(h.namespace).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		if err = fence(); err != nil {
			return err
		}
		_, err = h.k8s.BatchV1().CronJobs(h.namespace).Create(ctx, cj, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return err
	} else {
		if managementBackupObjectGeneration(existing.Annotations) > row.DesiredGeneration {
			return errManagementBackupStaleGeneration
		}
		cj.ResourceVersion = existing.ResourceVersion
		if err = fence(); err != nil {
			return err
		}
		_, err = h.k8s.BatchV1().CronJobs(h.namespace).Update(ctx, cj, metav1.UpdateOptions{})
		return err
	}
}

func (h *AdminDrillHandler) destinationLabels(id uuid.UUID) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":      "astronomer",
		"app.kubernetes.io/instance":  h.resourcePrefix(),
		"app.kubernetes.io/component": managementBackupComponent,
		"app.kubernetes.io/part-of":   "astronomer",
		destinationIDLabel:            id.String(),
	}
	return labels
}

func (h *AdminDrillHandler) buildDestinationCronJob(ctx context.Context, row sqlc.ManagementBackupDestination) *batchv1.CronJob {
	labels := h.destinationLabels(row.ID)
	sa := h.serviceAccount
	if sa == "" {
		sa = h.resourcePrefix()
	}
	image := h.backupImage
	if image == "" {
		image = defaultManagementBackupImage
	}
	nonRoot := int64(65534)
	falseVal := false
	env := []corev1.EnvVar{
		{Name: "HOME", Value: "/tmp"},
		{Name: "MANAGEMENT_BACKUP_RELEASE", Value: h.resourcePrefix()},
		{Name: "MANAGEMENT_BACKUP_DEST_NAME", Value: row.Name},
		{Name: "MANAGEMENT_BACKUP_BUCKET", Value: row.Bucket},
		{Name: "MANAGEMENT_BACKUP_REGION", Value: row.Region},
		{Name: "MANAGEMENT_BACKUP_PREFIX", Value: row.Prefix},
		{Name: "MANAGEMENT_BACKUP_KEEP_DAILY", Value: strconv.Itoa(int(row.KeepDaily))},
		{Name: "MANAGEMENT_BACKUP_KEEP_WEEKLY", Value: strconv.Itoa(int(row.KeepWeekly))},
		{Name: "MANAGEMENT_BACKUP_KEEP_MONTHLY", Value: strconv.Itoa(int(row.KeepMonthly))},
		h.databaseURLEnv(ctx),
	}
	if row.EndpointUrl != "" {
		env = append(env, corev1.EnvVar{Name: "MANAGEMENT_BACKUP_ENDPOINT", Value: row.EndpointUrl})
	}
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.destinationResourceName(row.ID),
			Namespace: h.namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"astronomer.io/management-backup-generation": strconv.FormatInt(row.DesiredGeneration, 10),
			},
		},
		Spec: batchv1.CronJobSpec{
			Schedule:                   row.Schedule,
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			SuccessfulJobsHistoryLimit: destInt32Ptr(3),
			FailedJobsHistoryLimit:     destInt32Ptr(3),
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: batchv1.JobSpec{
					BackoffLimit:            destInt32Ptr(2),
					TTLSecondsAfterFinished: destInt32Ptr(86400),
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: labels},
						Spec: corev1.PodSpec{
							RestartPolicy:      corev1.RestartPolicyOnFailure,
							ServiceAccountName: sa,
							SecurityContext: &corev1.PodSecurityContext{
								RunAsNonRoot: destBoolPtr(true),
								RunAsUser:    &nonRoot,
								RunAsGroup:   &nonRoot,
								FSGroup:      &nonRoot,
							},
							Containers: []corev1.Container{{
								Name:    "pgdump-s3",
								Image:   image,
								Command: []string{"/bin/sh", "-eu", "-c"},
								Args:    []string{managementBackupDumpScript},
								Env:     env,
								SecurityContext: &corev1.SecurityContext{
									AllowPrivilegeEscalation: &falseVal,
									ReadOnlyRootFilesystem:   destBoolPtr(true),
									Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								},
								VolumeMounts: []corev1.VolumeMount{
									{Name: "aws-credentials", MountPath: "/var/run/aws", ReadOnly: true},
									{Name: "scratch", MountPath: "/tmp"},
								},
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("100m"),
										corev1.ResourceMemory: resource.MustParse("256Mi"),
									},
									Limits: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("1000m"),
										corev1.ResourceMemory: resource.MustParse("1Gi"),
									},
								},
							}},
							Volumes: []corev1.Volume{
								{
									Name: "aws-credentials",
									VolumeSource: corev1.VolumeSource{
										Secret: &corev1.SecretVolumeSource{
											SecretName: h.destinationResourceName(row.ID) + "-aws",
											Items:      []corev1.KeyToPath{{Key: "credentials", Path: "credentials"}},
										},
									},
								},
								{
									Name: "scratch",
									VolumeSource: corev1.VolumeSource{
										EmptyDir: &corev1.EmptyDirVolumeSource{
											SizeLimit: resourcePtr("8Gi"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (h *AdminDrillHandler) databaseURLEnv(ctx context.Context) corev1.EnvVar {
	fallback := corev1.EnvVar{
		Name: "DATABASE_URL",
		ValueFrom: &corev1.EnvVarSource{
			ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: h.resourcePrefix() + "-config"},
				Key:                  "DATABASE_URL",
			},
		},
	}
	if h.k8s == nil {
		return fallback
	}
	dep, err := h.k8s.AppsV1().Deployments(h.namespace).Get(ctx, h.resourcePrefix()+"-server", metav1.GetOptions{})
	if err != nil {
		return fallback
	}
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name != "server" {
			continue
		}
		for _, e := range c.Env {
			if e.Name == "DATABASE_URL" {
				return e
			}
		}
	}
	return fallback
}

func (h *AdminDrillHandler) probeManagementBackupS3(ctx context.Context, row sqlc.ManagementBackupDestination, accessKey, secretKey string) error {
	endpoint := strings.TrimSpace(row.EndpointUrl)
	region := row.Region
	if region == "" {
		region = "us-east-1"
	}
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://s3.%s.amazonaws.com", region)
	}
	host, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint url")
	}
	host.Path = strings.TrimRight(host.Path, "/") + "/" + row.Bucket + "/"
	q := host.Query()
	q.Set("list-type", "2")
	q.Set("max-keys", "1")
	host.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host.String(), nil)
	if err != nil {
		return err
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
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusPartialContent:
		return nil
	case http.StatusForbidden:
		return fmt.Errorf("forbidden (likely invalid credentials)")
	case http.StatusNotFound:
		return fmt.Errorf("bucket not found: %s", row.Bucket)
	default:
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func destInt32Ptr(v int32) *int32 { return &v }

func destBoolPtr(v bool) *bool { return &v }

func managementBackupObjectGeneration(annotations map[string]string) int64 {
	if annotations == nil {
		return 0
	}
	generation, err := strconv.ParseInt(annotations["astronomer.io/management-backup-generation"], 10, 64)
	if err != nil || generation < 0 {
		return 0
	}
	return generation
}

func resourcePtr(v string) *resource.Quantity {
	q := resource.MustParse(v)
	return &q
}

// ReconcileManagementBackup is the generation-fenced worker boundary. It is
// the only path allowed to create/delete management-cluster backup resources.
func (h *AdminDrillHandler) ReconcileManagementBackup(ctx context.Context, id uuid.UUID, generation int64) error {
	q, ok := h.queries.(managementBackupWorkerQuerier)
	if !ok {
		return errors.New("management backup worker store is not configured")
	}
	row, err := q.ClaimManagementBackupDestinationGeneration(ctx, sqlc.ClaimManagementBackupDestinationGenerationParams{ID: id, Generation: generation})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := q.GetManagementBackupDestination(ctx, id)
		return managementBackupReconcileClaimMiss(existing, generation, loadErr)
	}
	if err != nil {
		return err
	}
	if h.k8s == nil || h.namespace == "" {
		return h.failManagementBackupGeneration(ctx, row, errors.New("management Kubernetes runtime unavailable"))
	}
	fence := func() error {
		current, loadErr := q.GetManagementBackupDestination(ctx, id)
		if loadErr != nil {
			return loadErr
		}
		if current.DesiredGeneration != generation || current.ReconcileStatus != "applying" {
			return errManagementBackupStaleGeneration
		}
		return nil
	}
	if row.DesiredState == "deleted" || !row.Enabled {
		err = h.deleteDestinationResources(ctx, id, generation, fence)
	} else {
		var access, secret string
		access, secret, err = h.decryptDestinationCredentials(row)
		if err == nil {
			err = h.reconcileDestination(ctx, row, access, secret, fence)
		}
	}
	if errors.Is(err, errManagementBackupStaleGeneration) {
		return nil
	}
	if err != nil {
		return h.failManagementBackupGeneration(ctx, row, err)
	}
	_, err = q.CompleteManagementBackupDestinationGeneration(ctx, sqlc.CompleteManagementBackupDestinationGenerationParams{ID: id, Generation: generation})
	return err
}

func (h *AdminDrillHandler) failManagementBackupGeneration(ctx context.Context, row sqlc.ManagementBackupDestination, effectErr error) error {
	q, ok := h.queries.(managementBackupWorkerQuerier)
	if !ok {
		return errors.New("management backup worker store is not configured")
	}
	category := sanitizedManagementBackupError(effectErr)
	terminal := managementBackupTerminalCategory(category) || managementBackupRetryExhausted(ctx)
	var persistErr error
	if terminal {
		_, persistErr = q.FailManagementBackupDestinationGeneration(ctx, sqlc.FailManagementBackupDestinationGenerationParams{ErrorMessage: category, ID: row.ID, Generation: row.DesiredGeneration})
	} else {
		_, persistErr = q.RetryManagementBackupDestinationGeneration(ctx, sqlc.RetryManagementBackupDestinationGenerationParams{ErrorMessage: category, ID: row.ID, Generation: row.DesiredGeneration})
	}
	if persistErr != nil {
		return fmt.Errorf("management backup %s; persist status failed", category)
	}
	if terminal {
		return fmt.Errorf("%w: management backup %s", asynq.SkipRetry, category)
	}
	return errors.New(category)
}

// ExecuteManagementBackupOperation claims a durable test/run receipt with a
// lease longer than the task timeout, then records a truthful terminal state.
func (h *AdminDrillHandler) ExecuteManagementBackupOperation(ctx context.Context, operationID uuid.UUID) error {
	q, ok := h.queries.(managementBackupWorkerQuerier)
	if !ok {
		return errors.New("management backup worker store is not configured")
	}
	op, err := q.ClaimManagementBackupOperation(ctx, operationID)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := q.GetWorkloadOperation(ctx, operationID)
		return managementBackupOperationClaimMiss(existing, loadErr)
	}
	if err != nil {
		return err
	}
	destinationID, err := uuid.Parse(op.TargetKey)
	if err == nil {
		row, loadErr := q.GetManagementBackupDestination(ctx, destinationID)
		err = loadErr
		if err == nil && row.DesiredState == "deleted" {
			err = errors.New("destination is deleted")
		}
		if err == nil && op.OperationType == "management_backup_test" {
			var access, secret string
			access, secret, err = h.decryptDestinationCredentials(row)
			if err == nil {
				err = h.probeManagementBackupS3(ctx, row, access, secret)
			}
		} else if err == nil && op.OperationType == "management_backup_run" {
			if row.ReconcileStatus != "ready" || row.AppliedGeneration != row.DesiredGeneration {
				err = errors.New("destination is not reconciled")
			} else if h.k8s == nil || h.namespace == "" {
				err = errors.New("management Kubernetes runtime unavailable")
			} else {
				var cj *batchv1.CronJob
				cj, err = h.destinationCronJob(ctx, destinationID)
				if err == nil && cj == nil {
					err = errors.New("backup CronJob is not ready")
				}
				if err == nil {
					err = h.executeManagementBackupRun(ctx, operationID, destinationID, cj)
				}
			}
		}
	}
	if err != nil {
		category := sanitizedManagementBackupError(err)
		terminal := managementBackupTerminalCategory(category) || managementBackupRetryExhausted(ctx)
		var persistErr error
		if terminal {
			_, persistErr = q.MarkWorkloadOperationFailed(ctx, sqlc.MarkWorkloadOperationFailedParams{ID: operationID, AttemptCount: op.AttemptCount, ErrorMessage: category})
		} else {
			_, persistErr = q.MarkWorkloadOperationRetrying(ctx, sqlc.MarkWorkloadOperationRetryingParams{ID: operationID, AttemptCount: op.AttemptCount, ErrorMessage: category})
		}
		if errors.Is(persistErr, pgx.ErrNoRows) {
			return nil
		}
		if persistErr != nil {
			return persistErr
		}
		if terminal {
			return fmt.Errorf("%w: management backup %s", asynq.SkipRetry, category)
		}
		return errors.New(category)
	}
	_, err = q.MarkWorkloadOperationCompleted(ctx, sqlc.MarkWorkloadOperationCompletedParams{ID: operationID, AttemptCount: op.AttemptCount})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

func (h *AdminDrillHandler) executeManagementBackupRun(ctx context.Context, operationID, destinationID uuid.UUID, cj *batchv1.CronJob) error {
	jobs := h.k8s.BatchV1().Jobs(h.namespace)
	jobName := "management-backup-op-" + operationID.String()
	job, err := jobs.Get(ctx, jobName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		// CronJob ForbidConcurrent only serializes Jobs created by that CronJob;
		// include scheduled and other manual Jobs in the destination fence.
		active, listErr := jobs.List(ctx, metav1.ListOptions{LabelSelector: destinationIDLabel + "=" + destinationID.String()})
		if listErr != nil {
			return listErr
		}
		for i := range active.Items {
			candidate := &active.Items[i]
			if candidate.Name != jobName && managementBackupJobOutcome(candidate) == "running" {
				return errors.New("backup_in_progress")
			}
		}
		job = &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: h.namespace, Labels: cj.Spec.JobTemplate.Labels}, Spec: cj.Spec.JobTemplate.Spec}
		job, err = jobs.Create(ctx, job, metav1.CreateOptions{})
	}
	if err != nil {
		return err
	}
	deadline := time.NewTimer(4 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		switch managementBackupJobOutcome(job) {
		case "succeeded":
			return nil
		case "failed":
			return errors.New("backup_job_failed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("backup_in_progress")
		case <-ticker.C:
			job, err = jobs.Get(ctx, jobName, metav1.GetOptions{})
			if err != nil {
				return err
			}
		}
	}
}

func managementBackupReconcileClaimMiss(row sqlc.ManagementBackupDestination, generation int64, loadErr error) error {
	if errors.Is(loadErr, pgx.ErrNoRows) {
		return nil
	}
	if loadErr != nil {
		return errors.New("load management backup destination failed")
	}
	if row.DesiredGeneration != generation {
		return nil
	}
	if row.AppliedGeneration >= generation && row.ReconcileStatus == "ready" {
		return nil
	}
	if row.ReconcileStatus == "failed" {
		return nil
	}
	return errors.New("management backup reconciliation lease is held")
}

func managementBackupOperationClaimMiss(operation sqlc.WorkloadOperation, loadErr error) error {
	if errors.Is(loadErr, pgx.ErrNoRows) {
		return nil
	}
	if loadErr != nil {
		return errors.New("load management backup operation failed")
	}
	switch operation.Status {
	case "completed", "failed", "cancelled", "superseded":
		return nil
	default:
		return errors.New("management backup operation lease is held")
	}
}

func managementBackupRetryExhausted(ctx context.Context) bool {
	retried, retryOK := asynq.GetRetryCount(ctx)
	maximum, maximumOK := asynq.GetMaxRetry(ctx)
	return retryOK && maximumOK && retried >= maximum
}

func managementBackupTerminalCategory(category string) bool {
	switch category {
	case "credentials_unavailable", "external_forbidden", "external_not_found", "backup_job_failed":
		return true
	default:
		return false
	}
}

func sanitizedManagementBackupError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "backup_in_progress"):
		return "backup_in_progress"
	case strings.Contains(msg, "backup_job_failed"):
		return "backup_job_failed"
	case strings.Contains(msg, "credential"), strings.Contains(msg, "encrypt"), strings.Contains(msg, "decrypt"):
		return "credentials_unavailable"
	case strings.Contains(msg, "not reconciled"), strings.Contains(msg, "cronjob is not ready"):
		return "destination_not_ready"
	case strings.Contains(msg, "kubernetes runtime unavailable"), strings.Contains(msg, "connectivity failed"), strings.Contains(msg, "timeout"):
		return "external_unreachable"
	case strings.Contains(msg, "forbidden"):
		return "external_forbidden"
	case strings.Contains(msg, "not found"):
		return "external_not_found"
	case strings.Contains(msg, "unexpected status"):
		return "external_http_error"
	default:
		return "internal_error"
	}
}

func managementBackupJobOutcome(job *batchv1.Job) string {
	if job == nil {
		return "running"
	}
	for _, condition := range job.Status.Conditions {
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		switch condition.Type {
		case batchv1.JobComplete:
			return "succeeded"
		case batchv1.JobFailed:
			return "failed"
		}
	}
	if job.Status.Succeeded > 0 {
		return "succeeded"
	}
	return "running"
}
