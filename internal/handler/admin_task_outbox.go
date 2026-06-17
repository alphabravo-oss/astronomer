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

type AdminTaskOutboxHandler struct {
	queries AdminTaskOutboxQuerier
	now     func() time.Time
}

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
	existing, err := h.queries.GetTaskOutbox(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Task outbox row not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	if existing.Status == "delivered" {
		RespondRequestError(w, r, http.StatusConflict, apierror.AlreadyDelivered, "Delivered task outbox rows cannot be retried")
		return
	}
	now := h.now
	if now == nil {
		now = time.Now
	}
	row, err := h.queries.RetryTaskOutbox(r.Context(), sqlc.RetryTaskOutboxParams{
		ID:            id,
		NextAttemptAt: pgtype.Timestamptz{Time: now().UTC(), Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusConflict, apierror.AlreadyDelivered, "Delivered task outbox rows cannot be retried")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.RetryError, err.Error())
		return
	}
	recordAudit(r, h.queries, "admin.task_outbox.retry", "task_outbox", id.String(), existing.TaskType, map[string]any{
		"previous_status": existing.Status,
		"task_type":       existing.TaskType,
		"queue_name":      existing.QueueName,
	})
	RespondJSON(w, http.StatusAccepted, taskOutboxToWire(row))
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
