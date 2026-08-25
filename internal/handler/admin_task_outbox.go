package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

type AdminTaskOutboxQuerier interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
	ListTaskOutbox(ctx context.Context, arg sqlc.ListTaskOutboxParams) ([]sqlc.TaskOutbox, error)
	CountTaskOutbox(ctx context.Context, status string) (int64, error)
	GetTaskOutbox(ctx context.Context, id uuid.UUID) (sqlc.TaskOutbox, error)
	RetryTaskOutbox(ctx context.Context, arg sqlc.RetryTaskOutboxParams) (sqlc.TaskOutbox, error)
}

type AdminTaskOutboxMutationTx interface {
	AdminTaskOutboxQuerier
	GetTaskOutboxForUpdate(context.Context, uuid.UUID) (sqlc.TaskOutbox, error)
	audit.OutboxQuerier
}

type adminTaskOutboxRunTxFunc func(context.Context, func(AdminTaskOutboxMutationTx) error) error

var errTaskOutboxAlreadyDelivered = errors.New("task outbox row already delivered")

type AdminTaskOutboxHandler struct {
	queries AdminTaskOutboxQuerier
	now     func() time.Time
	runTx   adminTaskOutboxRunTxFunc
}

func (h *AdminTaskOutboxHandler) SetRunTx(runTx adminTaskOutboxRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *AdminTaskOutboxHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

func NewAdminTaskOutboxHandler(queries AdminTaskOutboxQuerier) *AdminTaskOutboxHandler {
	return &AdminTaskOutboxHandler{queries: queries, now: time.Now}
}

type TaskOutboxResponse struct {
	ID                  string     `json:"id"`
	DedupeKey           string     `json:"dedupe_key,omitempty"`
	TaskType            string     `json:"task_type"`
	QueueName           string     `json:"queue_name"`
	MaxRetry            int32      `json:"max_retry"`
	TimeoutSeconds      int32      `json:"timeout_seconds"`
	UniqueSeconds       int32      `json:"unique_seconds"`
	MaxDeliveryAttempts int32      `json:"max_delivery_attempts"`
	Status              string     `json:"status"`
	AttemptCount        int32      `json:"attempt_count"`
	NextAttemptAt       *time.Time `json:"next_attempt_at,omitempty"`
	LockedUntil         *time.Time `json:"locked_until,omitempty"`
	DeliveredAt         *time.Time `json:"delivered_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	PayloadSize         int        `json:"payload_size"`
	CreatedAt           *time.Time `json:"created_at,omitempty"`
	UpdatedAt           *time.Time `json:"updated_at,omitempty"`
}

// List handles GET /api/v1/admin/task-outbox/.
func (h *AdminTaskOutboxHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && !validTaskOutboxStatus(status) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidStatus, "Invalid task outbox status")
		return
	}
	limit, offset := queryLimitOffset(r, 50)
	rows, err := h.queries.ListTaskOutbox(r.Context(), sqlc.ListTaskOutboxParams{
		Status: status,
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	total, err := h.queries.CountTaskOutbox(r.Context(), status)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	out := make([]TaskOutboxResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, taskOutboxToWire(row))
	}
	RespondPaginated(w, r, out, total)
}

// ListDead handles GET /api/v1/admin/task-outbox/dead/ — the dead-letter view.
// It is the status=dead slice of List, exposed as a dedicated path so operators
// (and the runbook) have a stable URL for the DLQ without query-string fiddling.
func (h *AdminTaskOutboxHandler) ListDead(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	limit, offset := queryLimitOffset(r, 50)
	rows, err := h.queries.ListTaskOutbox(r.Context(), sqlc.ListTaskOutboxParams{
		Status: "dead",
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	total, err := h.queries.CountTaskOutbox(r.Context(), "dead")
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	out := make([]TaskOutboxResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, taskOutboxToWire(row))
	}
	RespondPaginated(w, r, out, total)
}

// Get returns the exact durable row referenced by retry receipts.
func (h *AdminTaskOutboxHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid task outbox ID")
		return
	}
	row, err := h.queries.GetTaskOutbox(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Task outbox row not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to load task outbox row")
		return
	}
	RespondJSON(w, http.StatusOK, taskOutboxToWire(row))
}

// Retry handles POST /api/v1/admin/task-outbox/{id}/retry/.
func (h *AdminTaskOutboxHandler) Retry(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid task outbox ID")
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "task outbox transaction runner is not configured")
		return
	}
	r = r.WithContext(withOperationIdempotency(r, "admin_task_outbox_retry"))
	digest, err := canonicalOperationRequestDigest(struct {
		Action string `json:"action"`
		ID     string `json:"id"`
	}{Action: "retry", ID: id.String()})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode task outbox retry request")
		return
	}
	now := h.now
	if now == nil {
		now = time.Now
	}
	params := sqlc.RetryTaskOutboxParams{ID: id, NextAttemptAt: pgtype.Timestamptz{Time: now().UTC(), Valid: true}}
	var existing, row sqlc.TaskOutbox
	var receipt TaskOutboxResponse
	err = h.runTx(r.Context(), func(q AdminTaskOutboxMutationTx) error {
		idemQ, ok := q.(resourceOperationIdempotencyQuerier)
		if !ok {
			return errors.New("task outbox idempotency store is not configured")
		}
		_, stored, replay, claimErr := claimOperationReceipt[TaskOutboxResponse](r.Context(), idemQ, "task_outbox_retries", digest)
		if claimErr != nil {
			return claimErr
		}
		if replay {
			receipt = stored
			return nil
		}
		var getErr error
		existing, getErr = q.GetTaskOutboxForUpdate(r.Context(), id)
		if getErr != nil {
			return getErr
		}
		if existing.Status == "delivered" {
			return errTaskOutboxAlreadyDelivered
		}
		row, getErr = q.RetryTaskOutbox(r.Context(), params)
		if getErr != nil {
			return getErr
		}
		receipt = taskOutboxToWire(row)
		if auditErr := recordAuditOutbox(r, q, "admin.task_outbox.retry", "task_outbox", id.String(), existing.TaskType, http.StatusAccepted, map[string]any{
			"previous_status": existing.Status, "task_type": existing.TaskType, "queue_name": existing.QueueName,
		}); auditErr != nil {
			return auditErr
		}
		return attachOperationReceipt(r.Context(), idemQ, "task_outbox_retries", row.ID, digest, receipt)
	})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different task outbox retry")
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Task outbox row not found")
			return
		}
		if errors.Is(err, errTaskOutboxAlreadyDelivered) {
			RespondRequestError(w, r, http.StatusConflict, apierror.AlreadyDelivered, "Delivered task outbox rows cannot be retried")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.RetryError, "Failed to retry task outbox row")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/admin/task-outbox/"+receipt.ID+"/", receipt)
}

func (h *AdminTaskOutboxHandler) gate(w http.ResponseWriter, r *http.Request) bool {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.DBUnavailable, "task outbox database not wired")
		return false
	}
	_, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{
		InvalidUserStatus:  http.StatusForbidden,
		InvalidUserCode:    "forbidden",
		InvalidUserMessage: "Invalid caller",
		ForbiddenMessage:   "superuser required",
	})
	return ok
}

func validTaskOutboxStatus(status string) bool {
	switch status {
	case "", "pending", "delivering", "failed", "delivered", "dead":
		return true
	default:
		return false
	}
}

func taskOutboxToWire(row sqlc.TaskOutbox) TaskOutboxResponse {
	resp := TaskOutboxResponse{
		ID:                  row.ID.String(),
		TaskType:            row.TaskType,
		QueueName:           row.QueueName,
		MaxRetry:            row.MaxRetry,
		TimeoutSeconds:      row.TimeoutSeconds,
		UniqueSeconds:       row.UniqueSeconds,
		MaxDeliveryAttempts: row.MaxDeliveryAttempts,
		Status:              row.Status,
		AttemptCount:        row.AttemptCount,
		LastError:           row.LastError,
		PayloadSize:         len(row.Payload),
	}
	if row.DedupeKey.Valid {
		resp.DedupeKey = row.DedupeKey.String
	}
	resp.NextAttemptAt = taskOutboxTimePtr(row.NextAttemptAt)
	resp.LockedUntil = pgTimePtr(row.LockedUntil)
	resp.DeliveredAt = pgTimePtr(row.DeliveredAt)
	resp.CreatedAt = taskOutboxTimePtr(row.CreatedAt)
	resp.UpdatedAt = taskOutboxTimePtr(row.UpdatedAt)
	return resp
}

func taskOutboxTimePtr(v time.Time) *time.Time {
	if v.IsZero() {
		return nil
	}
	t := v.UTC()
	return &t
}

func pgTimePtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time.UTC()
	return &t
}
