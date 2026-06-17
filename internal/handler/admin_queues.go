// Package handler — admin queue inspector.
//
// Superuser-only endpoint that exposes the asynq
// queue state (depths, DLQ contents, active tasks, retry counts) as JSON
// so an operator can answer "why isn't anything reconciling?" from the
// UI / curl instead of shelling into a worker pod for the asynq CLI.
//
// Two surfaces:
//
//	GET /api/v1/admin/queues/                     — summary across queues
//	GET /api/v1/admin/queues/{queue}/dlq/         — recent DLQ entries
//
// The handler uses the same SupportBundleAsynqInspector interface the
// support-bundle code added; it's the smallest dependency that *asynq.Inspector satisfies.
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// AdminQueuesQuerier is the slice of sqlc.Queries the handler needs.
// Superuser gate + audit writer for the mutating DLQ endpoints.
type AdminQueuesQuerier interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
}

// AdminQueuesHandler wraps GET /api/v1/admin/queues/*.
type AdminQueuesHandler struct {
	inspector SupportBundleAsynqInspector
	queries   AdminQueuesQuerier
}

// NewAdminQueuesHandler builds a handler. inspector + queries are
// required for a usable handler; nil inspector renders a clean 503.
func NewAdminQueuesHandler(inspector SupportBundleAsynqInspector, queries AdminQueuesQuerier) *AdminQueuesHandler {
	return &AdminQueuesHandler{inspector: inspector, queries: queries}
}

// QueueSummary is the wire shape returned by GET /admin/queues/.
type QueueSummary struct {
	Name      string    `json:"name"`
	Size      int       `json:"size"`
	Active    int       `json:"active"`
	Pending   int       `json:"pending"`
	Scheduled int       `json:"scheduled"`
	Retry     int       `json:"retry"`
	Archived  int       `json:"archived"`
	Completed int       `json:"completed"`
	Paused    bool      `json:"paused"`
	AsOf      time.Time `json:"as_of"`
}

// List handles GET /api/v1/admin/queues/.
func (h *AdminQueuesHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.inspector == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InspectorUnavailable, "asynq inspector not wired")
		return
	}
	queues, err := h.inspector.Queues()
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.AsynqError, err.Error())
		return
	}
	now := time.Now().UTC()
	out := make([]QueueSummary, 0, len(queues))
	for _, q := range queues {
		info, ierr := h.inspector.GetQueueInfo(q)
		if ierr != nil {
			// Surface one bad queue without nuking the whole listing.
			out = append(out, QueueSummary{Name: q, AsOf: now})
			continue
		}
		out = append(out, QueueSummary{
			Name:      q,
			Size:      info.Size,
			Active:    info.Active,
			Pending:   info.Pending,
			Scheduled: info.Scheduled,
			Retry:     info.Retry,
			Archived:  info.Archived,
			Completed: info.Completed,
			Paused:    info.Paused,
			AsOf:      now,
		})
	}
	RespondJSON(w, http.StatusOK, out)
}

// DLQEntry is the wire shape for a single archived task.
type DLQEntry struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Retried      int       `json:"retried"`
	LastErr      string    `json:"last_err"`
	LastFailedAt time.Time `json:"last_failed_at"`
}

// DLQ handles GET /api/v1/admin/queues/{queue}/dlq/.
func (h *AdminQueuesHandler) DLQ(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.inspector == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InspectorUnavailable, "asynq inspector not wired")
		return
	}
	queueName := chi.URLParam(r, "queue")
	if queueName == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.QueueRequired, "queue name is required")
		return
	}
	// Page size of 100 — big enough to spot patterns, small enough to
	// keep the response under a scrolled UI panel.
	archived, err := h.inspector.ListArchivedTasks(queueName, asynq.PageSize(100))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.AsynqError, err.Error())
		return
	}
	out := make([]DLQEntry, 0, len(archived))
	for _, t := range archived {
		out = append(out, DLQEntry{
			ID:           t.ID,
			Type:         t.Type,
			Retried:      t.Retried,
			LastErr:      t.LastErr,
			LastFailedAt: t.LastFailedAt,
		})
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"queue": queueName,
		"dlq":   out,
		"count": len(out),
	})
}

// RetryDLQ handles POST /api/v1/admin/queues/{queue}/dlq/{id}/retry/.
//
// Moves an archived (DLQ) task back to the pending queue so asynq
// re-attempts it. Wraps asynq.Inspector.RunTask — the upstream
// guarantee is idempotent: a task that's already pending becomes a
// no-op. Superuser-gated; emits an audit row.
func (h *AdminQueuesHandler) RetryDLQ(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.inspector == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InspectorUnavailable, "asynq inspector not wired")
		return
	}
	queue := chi.URLParam(r, "queue")
	id := chi.URLParam(r, "id")
	if queue == "" || id == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.MissingParams, "queue and id are required")
		return
	}
	if err := h.inspector.RunTask(queue, id); err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.AsynqError, err.Error())
		return
	}
	recordAudit(r, h.queries, "admin.queue.dlq_retried", "queue", queue, id, map[string]any{
		"task_id": id,
	})
	RespondJSON(w, http.StatusAccepted, map[string]any{"queue": queue, "task_id": id, "retried": true})
}

// DiscardDLQ handles DELETE /api/v1/admin/queues/{queue}/dlq/{id}/.
//
// Removes an archived task from the DLQ entirely. Used to clear
// permanently-broken jobs (poison messages, deleted clusters, etc.)
// that retry would only repeat the failure on. Superuser-gated.
func (h *AdminQueuesHandler) DiscardDLQ(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.inspector == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InspectorUnavailable, "asynq inspector not wired")
		return
	}
	queue := chi.URLParam(r, "queue")
	id := chi.URLParam(r, "id")
	if queue == "" || id == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.MissingParams, "queue and id are required")
		return
	}
	if err := h.inspector.DeleteTask(queue, id); err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.AsynqError, err.Error())
		return
	}
	recordAudit(r, h.queries, "admin.queue.dlq_discarded", "queue", queue, id, map[string]any{
		"task_id": id,
	})
	RespondJSON(w, http.StatusOK, map[string]any{"queue": queue, "task_id": id, "discarded": true})
}

// gate enforces superuser-only access and emits the admin audit row.
// Returns true if the request may proceed; emits 401/403 otherwise.
func (h *AdminQueuesHandler) gate(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{
		StoreUnavailableStatus:  http.StatusInternalServerError,
		StoreUnavailableCode:    "internal_error",
		StoreUnavailableMessage: "User store not configured",
		ForbiddenMessage:        "Queue inspector requires superuser privileges",
	}); !ok {
		return false
	}
	// Audit trail — same pattern as key-status + support-bundle.
	recordAudit(r, h.queries, "admin.queues.viewed", "platform", "", "queues", map[string]any{
		"path": r.URL.Path,
	})
	return true
}
