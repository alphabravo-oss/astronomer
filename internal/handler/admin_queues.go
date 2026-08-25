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
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// AdminQueuesQuerier is the slice of sqlc.Queries the handler needs.
// Superuser gate + audit writer for the mutating DLQ endpoints.
type AdminQueuesQuerier interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
	GetAdminQueueOperation(ctx context.Context, id uuid.UUID) (sqlc.AdminQueueOperation, error)
}

// AdminQueueMutationTx is deliberately satisfied by transaction-bound sqlc
// queries only. The operation receipt, delivery intent, and audit intent are a
// single commit decision; Redis is never touched from this interface.
type AdminQueueMutationTx interface {
	CreateAdminQueueOperation(context.Context, sqlc.CreateAdminQueueOperationParams) (sqlc.AdminQueueOperation, error)
	tasks.TaskOutboxWriter
	audit.OutboxQuerier
}

type adminQueueRunTxFunc func(context.Context, func(AdminQueueMutationTx) error) error

var errAdminQueueConflictingOperation = errors.New("conflicting admin queue operation is active")

// AdminQueuesHandler wraps GET /api/v1/admin/queues/*.
type AdminQueuesHandler struct {
	inspector SupportBundleAsynqInspector
	queries   AdminQueuesQuerier
	bus       *events.Bus
	runTx     adminQueueRunTxFunc
}

func (h *AdminQueuesHandler) SetRunTx(runTx adminQueueRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *AdminQueuesHandler) TransactionalMutationWired() bool {
	return h != nil && h.runTx != nil
}

// SetEventBus wires the SSE bus for admin_queue.changed liveness events
// (P4.5). Deliberately unscoped (no cluster_id): the SEC-R07 fail-closed
// drop makes it superuser-only, matching the endpoints (D9).
func (h *AdminQueuesHandler) SetEventBus(bus *events.Bus) {
	if h == nil {
		return
	}
	h.bus = bus
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
// Persists a durable request to move an archived task back to pending. The
// worker owns the Redis mutation; this method returns the committed operation
// receipt and never claims the external effect has already happened.
func (h *AdminQueuesHandler) RetryDLQ(w http.ResponseWriter, r *http.Request) {
	h.createOperation(w, r, "retry")
}

// DiscardDLQ handles DELETE /api/v1/admin/queues/{queue}/dlq/{id}/.
//
// Persists a durable request to remove an archived task. The worker owns the
// Redis mutation and treats an already-absent target as the desired state.
func (h *AdminQueuesHandler) DiscardDLQ(w http.ResponseWriter, r *http.Request) {
	h.createOperation(w, r, "discard")
}

// GetOperation returns the durable status behind a retry/discard receipt.
func (h *AdminQueuesHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "invalid admin queue operation ID")
		return
	}
	row, err := h.queries.GetAdminQueueOperation(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "admin queue operation not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "failed to load admin queue operation")
		return
	}
	RespondJSON(w, http.StatusOK, adminQueueOperationToWire(row))
}

func (h *AdminQueuesHandler) createOperation(w http.ResponseWriter, r *http.Request, action string) {
	if !h.gate(w, r) {
		return
	}
	queue := chi.URLParam(r, "queue")
	taskID := chi.URLParam(r, "id")
	if queue == "" || taskID == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.MissingParams, "queue and id are required")
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "admin queue mutation runner is not configured")
		return
	}
	caller := currentUserUUID(r)
	if !caller.Valid || caller.Bytes == uuid.Nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "invalid caller")
		return
	}
	r = r.WithContext(withOperationIdempotency(r, "admin_queue_dlq"))
	digest, err := canonicalOperationRequestDigest(struct {
		Action string `json:"action"`
		Queue  string `json:"queue"`
		TaskID string `json:"task_id"`
	}{Action: action, Queue: queue, TaskID: taskID})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "failed to encode admin queue request")
		return
	}

	var operation sqlc.AdminQueueOperation
	var receipt AdminQueueOperationResponse
	replayed := false
	err = h.runTx(r.Context(), func(q AdminQueueMutationTx) error {
		idemQ, ok := q.(resourceOperationIdempotencyQuerier)
		if !ok {
			return errors.New("admin queue idempotency store is not configured")
		}
		_, stored, replay, claimErr := claimOperationReceipt[AdminQueueOperationResponse](r.Context(), idemQ, "admin_queue_operations", digest)
		if claimErr != nil {
			return claimErr
		}
		if replay {
			receipt, replayed = stored, true
			return nil
		}
		var createErr error
		operation, createErr = q.CreateAdminQueueOperation(r.Context(), sqlc.CreateAdminQueueOperationParams{
			Action: action, QueueName: queue, TaskID: taskID, RequestedBy: caller.Bytes,
		})
		if createErr != nil {
			return createErr
		}
		if operation.Action != action {
			return errAdminQueueConflictingOperation
		}
		receipt = adminQueueOperationToWire(operation)
		task, taskErr := tasks.NewAdminQueueOperationTask(operation.ID)
		if taskErr != nil {
			return taskErr
		}
		if _, taskErr = tasks.EnqueueTaskOutbox(r.Context(), q, task, tasks.TaskOutboxOptions{
			DedupeKey: audit.MutationDedupeKey(strings.TrimSpace(r.Header.Get("Idempotency-Key")), action, queue, taskID),
			MaxRetry:  8, Timeout: time.Minute, MaxDeliveryAttempts: 20,
		}); taskErr != nil {
			return taskErr
		}
		if auditErr := recordAuditOutbox(r, q, "admin.queue.dlq_"+action+"_accepted", "admin_queue_operation", operation.ID.String(), action, http.StatusAccepted, map[string]any{
			"operation_id": operation.ID.String(), "queue": queue, "task_id": taskID, "action": action,
		}); auditErr != nil {
			return auditErr
		}
		return attachOperationReceipt(r.Context(), idemQ, "admin_queue_operations", operation.ID, digest, receipt)
	})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different admin queue operation")
			return
		}
		if errors.Is(err, errAdminQueueConflictingOperation) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "a conflicting operation is already active for this queue task")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "failed to persist admin queue operation")
		return
	}
	if !replayed {
		events.PublishChanged(h.bus, "admin_queue", "", queue, map[string]any{
			"action": "dlq_" + action + "_accepted", "operation_id": operation.ID.String(),
		})
	}
	RespondAcceptedOperation(w, "/api/v1/admin/queues/operations/"+receipt.ID+"/", receipt)
}

type AdminQueueOperationResponse struct {
	ID           string     `json:"id"`
	Action       string     `json:"action"`
	Queue        string     `json:"queue"`
	TaskID       string     `json:"task_id"`
	Status       string     `json:"status"`
	AttemptCount int32      `json:"attempt_count"`
	LastError    string     `json:"last_error,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func adminQueueOperationToWire(row sqlc.AdminQueueOperation) AdminQueueOperationResponse {
	out := AdminQueueOperationResponse{
		ID: row.ID.String(), Action: row.Action, Queue: row.QueueName, TaskID: row.TaskID,
		Status: row.Status, AttemptCount: row.AttemptCount, LastError: row.LastError,
		CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
	if row.CompletedAt.Valid {
		completedAt := row.CompletedAt.Time.UTC()
		out.CompletedAt = &completedAt
	}
	return out
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
	// Read auditing belongs only to inspection requests. Mutations commit their
	// own audit intent atomically; recording a view here would duplicate an
	// audit row on a durable idempotent replay.
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		recordAudit(r, h.queries, "admin.queues.viewed", "platform", "", "queues", map[string]any{
			"path": r.URL.Path,
		})
	}
	return true
}
