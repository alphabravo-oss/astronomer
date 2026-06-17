// Cluster-registration wizard endpoints (sprint 22 / migration 078).
//
// Six routes mount under /api/v1/clusters/{id}/registration/*:
//
//	GET   .../status/        — full Status (phase + steps)
//	GET   .../events/        — SSE stream of cluster.registration.* events
//	PUT   .../options/       — operator's step-1 install_baseline choice
//	POST  .../confirm/       — operator clicked "I've run it" on wizard page 2
//	POST  .../retry/{step}/  — re-fire the underlying task for a failed step
//	POST  .../cancel/        — superuser abort
//
// The phase machine itself lives in internal/registration/. This file
// is the HTTP shim — argument parsing, response shaping, audit.
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// ClusterRegistrationQuerier is the small DB surface the handler
// needs. Local interface so the wiring layer can inject *sqlc.Queries
// directly and the handler tests can supply a fake.
type ClusterRegistrationQuerier interface {
	registration.Querier
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	// Auto-attach for install_baseline=true at confirm time. We resolve
	// the template's spec via GetClusterTemplateByID and materialize it
	// into the cluster_template_applications row's snapshot column so
	// later template edits don't retroactively rewrite what was applied.
	GetClusterTemplateByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterTemplate, error)
	UpsertClusterTemplateApplication(ctx context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error)
}

type clusterRegistrationStepTaskOutboxQuerier interface {
	UpdateClusterRegistrationStepWithTaskOutbox(ctx context.Context, arg sqlc.UpdateClusterRegistrationStepWithTaskOutboxParams) (sqlc.ClusterRegistrationStep, error)
}

// ClusterRegistrationHandler bundles the wizard endpoints.
type ClusterRegistrationHandler struct {
	queries ClusterRegistrationQuerier
	service *registration.Service
	bus     *events.Bus
	// applyQueue enqueues the cluster_template:apply task when the
	// operator opted-in to install_baseline on /confirm/. Optional;
	// when nil the row is still upserted but the worker picks it up
	// only on the periodic sweep.
	applyQueue ClusterDecommissionEnqueuer
	// taskOutbox persists the apply task intent before Redis delivery.
	// Optional; when nil the handler falls back to direct enqueue for
	// compatibility with older tests and partial wiring.
	taskOutbox tasks.TaskOutboxWriter
	// argoCDAutoRegisterQueue enqueues argocd:auto_register_cluster when
	// operators retry a failed ArgoCD adoption step from the timeline.
	// Optional; taskOutbox is preferred when available.
	argoCDAutoRegisterQueue ClusterDecommissionEnqueuer
	// baselineTemplateID identifies the platform-baseline template
	// row that the wizard auto-attaches when the operator opts in.
	// Set via SetBaselineTemplateID; when uuid.Nil the auto-attach
	// is silently skipped (no platform-baseline template installed
	// in this deployment).
	baselineTemplateID uuid.UUID
	// auditQueries lets recordAudit write through. Same querier
	// works for both since *sqlc.Queries implements the broader
	// audit surface, so we just pass the same interface.
	auditQueries any
}

// busAdapter bridges *events.Bus (Publish(events.Type, any)) to the
// registration.Publisher interface (Publish(string, any)). Keeps the
// registration package free of an events import.
type busAdapter struct{ b *events.Bus }

func (a busAdapter) Publish(eventType string, data any) {
	if a.b == nil {
		return
	}
	a.b.Publish(events.Type(eventType), data)
}

// NewClusterRegistrationHandler constructs the handler.
func NewClusterRegistrationHandler(q ClusterRegistrationQuerier, bus *events.Bus) *ClusterRegistrationHandler {
	var pub registration.Publisher
	if bus != nil {
		pub = busAdapter{b: bus}
	}
	return &ClusterRegistrationHandler{
		queries:      q,
		service:      registration.New(q, pub),
		bus:          bus,
		auditQueries: q,
	}
}

// Service exposes the underlying registration.Service so other
// packages (tunnel hub, cluster_template:apply task) can share the
// same instance. Avoids constructing a second Service that would
// publish events through a different bus reference.
func (h *ClusterRegistrationHandler) Service() *registration.Service {
	if h == nil {
		return nil
	}
	return h.service
}

// SetApplyQueue wires the asynq client used by /confirm/ when the
// operator opted into the platform baseline. Optional / nil-safe.
func (h *ClusterRegistrationHandler) SetApplyQueue(q ClusterDecommissionEnqueuer) {
	if h == nil {
		return
	}
	h.applyQueue = q
}

// SetTaskOutbox wires the durable task outbox used before direct Redis
// enqueue. Optional / nil-safe.
func (h *ClusterRegistrationHandler) SetTaskOutbox(q tasks.TaskOutboxWriter) {
	if h == nil {
		return
	}
	h.taskOutbox = q
}

// SetArgoCDAutoRegisterQueue wires retry delivery for failed ArgoCD
// auto-adoption timeline steps. Optional / nil-safe.
func (h *ClusterRegistrationHandler) SetArgoCDAutoRegisterQueue(q ClusterDecommissionEnqueuer) {
	if h == nil {
		return
	}
	h.argoCDAutoRegisterQueue = q
}

// SetBaselineTemplateID records which cluster_templates row the
// wizard auto-attaches when install_baseline=true. The wiring layer
// looks it up by well-known name (e.g. "platform-baseline") at
// startup and passes the ID here.
func (h *ClusterRegistrationHandler) SetBaselineTemplateID(id uuid.UUID) {
	if h == nil {
		return
	}
	h.baselineTemplateID = id
}

// ────────────────────────────────────────────────────────────────────
// Endpoints
// ────────────────────────────────────────────────────────────────────

// GetStatus handles GET /clusters/{id}/registration/status/.
func (h *ClusterRegistrationHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	status, err := h.service.LoadStatus(r.Context(), id)
	if err != nil {
		if err == pgx.ErrNoRows {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LoadError, "Failed to load registration status")
		return
	}
	RespondJSON(w, http.StatusOK, status)
}

// PutOptions handles PUT /clusters/{id}/registration/options/.
// Body: {"install_baseline": bool}
func (h *ClusterRegistrationHandler) PutOptions(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	var req struct {
		InstallBaseline *bool `json:"install_baseline"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if req.InstallBaseline == nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "install_baseline is required")
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	if _, err := h.service.SetInstallBaseline(r.Context(), id, *req.InstallBaseline); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to record options")
		return
	}
	recordAudit(r, h.auditQueries, "cluster.registration.options", "cluster", id.String(), "", map[string]any{
		"install_baseline": *req.InstallBaseline,
	})
	status, err := h.service.LoadStatus(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LoadError, "Failed to load status")
		return
	}
	RespondJSON(w, http.StatusOK, status)
}

// PostConfirm handles POST /clusters/{id}/registration/confirm/.
// Advances the cluster from `created` → `awaiting_agent`, and (if the
// operator opted in to the baseline) upserts the auto-attach row +
// enqueues the apply task.
func (h *ClusterRegistrationHandler) PostConfirm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	record, advErr := h.service.Advance(r.Context(), id, registration.EventConfirm)
	if advErr != nil {
		if h.isIllegal(advErr) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, advErr.Error())
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TransitionError, advErr.Error())
		return
	}

	if record.InstallBaseline.Valid && record.InstallBaseline.Bool && h.baselineTemplateID != uuid.Nil {
		// Auto-attach the platform-baseline template. The apply worker
		// reads spec from the snapshot column, NOT from the template
		// row (so a template edit doesn't retroactively change what
		// was applied), so we must materialize the template's spec
		// into the application row here. Mirrors the legacy
		// ClusterHandler.autoAttachDefaultTemplate path.
		spec := json.RawMessage(`{}`)
		if tmpl, terr := h.queries.GetClusterTemplateByID(r.Context(), h.baselineTemplateID); terr == nil && len(tmpl.Spec) > 0 {
			spec = tmpl.Spec
		}
		appParams := sqlc.UpsertClusterTemplateApplicationParams{
			ClusterID:    id,
			TemplateID:   h.baselineTemplateID,
			SpecSnapshot: spec,
		}
		dedupeKey := fmt.Sprintf("cluster_registration:confirm:cluster_template_apply:%s", id.String())
		task := asynq.NewTask("cluster_template:apply", mustRegistrationJSON(map[string]any{"cluster_id": id.String()}))
		_, atomic, err := upsertClusterTemplateApplicationWithTaskOutbox(r.Context(), h.queries, h.taskOutbox, appParams, task, tasks.TaskOutboxOptions{
			DedupeKey:           dedupeKey,
			QueueName:           "tunnel",
			MaxRetry:            3,
			MaxDeliveryAttempts: 20,
		})
		if err != nil {
			h.recordTemplateAttachFailure(r.Context(), id, err)
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.AttachError, "Failed to attach platform baseline")
			return
		}
		if !atomic {
			if _, err := h.queries.UpsertClusterTemplateApplication(r.Context(), appParams); err != nil {
				h.recordTemplateAttachFailure(r.Context(), id, err)
				RespondRequestError(w, r, http.StatusInternalServerError, apierror.AttachError, "Failed to attach platform baseline")
				return
			}
			h.enqueueTemplateApply(r.Context(), id, dedupeKey)
		}
	}

	recordAudit(r, h.auditQueries, "cluster.registration.confirm", "cluster", id.String(), cluster.Name, map[string]any{
		"install_baseline": record.InstallBaseline.Valid && record.InstallBaseline.Bool,
	})

	status, _ := h.service.LoadStatus(r.Context(), id)
	RespondJSON(w, http.StatusOK, status)
}

// PostRetry handles POST /clusters/{id}/registration/retry/{step_id}/.
// Marks the failed step as a fresh retry and re-fires the underlying
// task. Only valid when the cluster is in `failed`.
func (h *ClusterRegistrationHandler) PostRetry(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	stepID, err := uuid.Parse(chi.URLParam(r, "step_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidStep, "Invalid step ID")
		return
	}
	step, err := h.queries.GetClusterRegistrationStep(r.Context(), stepID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Step not found")
		return
	}
	if step.ClusterID != id {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.StepMismatch, "Step does not belong to cluster")
		return
	}
	if step.Status != "failed" {
		RespondRequestError(w, r, http.StatusConflict, apierror.NotFailed, "Only failed steps can be retried")
		return
	}
	// Advance back to provisioning so the timeline doesn't lie about
	// being stuck at failed while the task is re-running.
	if _, err := h.service.Advance(r.Context(), id, registration.EventRetry); err != nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, err.Error())
		return
	}
	task, queueName, maxRetry, dedupeKey := h.registrationRetryTask(id, stepID, step.StepName)
	updatedStep, atomic, err := h.updateRetryStepWithTaskOutbox(r.Context(), stepID, task, dedupeKey, queueName, maxRetry)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.RetryError, "Failed to queue retry")
		return
	}
	if atomic {
		h.publishRegistrationStep(updatedStep)
	} else {
		h.enqueueRegistrationRetryTask(r.Context(), task, queueName, maxRetry, dedupeKey)
		// Mark the failing step as `pending` again so the UI shows it
		// as queued rather than the error remaining sticky.
		_, _ = h.service.UpdateStep(r.Context(), registration.UpdateStepInput{
			StepID:      stepID,
			Status:      "pending",
			ProgressPct: 0,
		})
	}
	recordAudit(r, h.auditQueries, "cluster.registration.retry", "cluster", id.String(), "", map[string]any{
		"step_id":   stepID.String(),
		"step_name": step.StepName,
	})
	status, _ := h.service.LoadStatus(r.Context(), id)
	RespondJSON(w, http.StatusOK, status)
}

// PostCancel handles POST /clusters/{id}/registration/cancel/. Superuser-only.
func (h *ClusterRegistrationHandler) PostCancel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if _, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{
		InvalidUserMessage: "Invalid caller ID",
		ForbiddenMessage:   "Cancel requires superuser",
	}); !ok {
		return
	}
	if _, err := h.service.Advance(r.Context(), id, registration.EventCancel,
		registration.WithError("cancelled by superuser")); err != nil {
		if h.isIllegal(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, err.Error())
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TransitionError, err.Error())
		return
	}
	recordAudit(r, h.auditQueries, "cluster.registration.cancel", "cluster", id.String(), "", nil)
	status, _ := h.service.LoadStatus(r.Context(), id)
	RespondJSON(w, http.StatusOK, status)
}

// StreamEvents handles GET /clusters/{id}/registration/events/. SSE
// stream filtered to the given cluster's registration.* events. The
// authentication is enforced by the route's middleware stack — we
// don't redo it here.
func (h *ClusterRegistrationHandler) StreamEvents(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if h.bus == nil {
		http.Error(w, "event stream not available", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	ch := h.bus.Subscribe(r.Context())
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if !matchesCluster(ev, id) {
				continue
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// matchesCluster returns true when the event payload references the
// given cluster_id. Events that don't carry a cluster_id (e.g. cross-
// cluster admin events) are filtered out.
func matchesCluster(ev events.Event, clusterID uuid.UUID) bool {
	// Skip anything that isn't registration / cluster-lifecycle
	switch ev.Type {
	case events.TypeClusterRegistrationStep,
		events.TypeClusterRegistrationPhase,
		events.TypeClusterConnected,
		events.TypeClusterDisconnected,
		events.TypeAgentReconnecting,
		events.TypeAgentFailed:
	default:
		return false
	}
	// Try common payload shapes.
	switch d := ev.Data.(type) {
	case map[string]any:
		if id, ok := d["cluster_id"].(string); ok {
			return id == clusterID.String()
		}
	}
	// Marshal-back path: payload is a typed struct (stepWriteResult,
	// phaseChangeResult) — marshal to JSON and re-parse.
	b, err := json.Marshal(ev.Data)
	if err != nil {
		return false
	}
	var probe struct {
		ClusterID string `json:"cluster_id"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return false
	}
	return probe.ClusterID == clusterID.String()
}

func (h *ClusterRegistrationHandler) isIllegal(err error) bool {
	if err == nil {
		return false
	}
	// errors.Is would chain through but we want a string-prefix check
	// to also tolerate fmt.Errorf-wrapped variants from Transition.
	if err == registration.ErrIllegalTransition {
		return true
	}
	s := err.Error()
	return len(s) >= len("illegal phase transition") && s[:len("illegal phase transition")] == "illegal phase transition"
}

func (h *ClusterRegistrationHandler) enqueueTemplateApply(ctx context.Context, clusterID uuid.UUID, dedupeKey string) {
	task := asynq.NewTask("cluster_template:apply",
		mustRegistrationJSON(map[string]any{"cluster_id": clusterID.String()}))
	if h.taskOutbox != nil {
		if _, err := tasks.EnqueueTaskOutbox(ctx, h.taskOutbox, task, tasks.TaskOutboxOptions{
			DedupeKey:           dedupeKey,
			QueueName:           "tunnel",
			MaxRetry:            3,
			MaxDeliveryAttempts: 20,
		}); err == nil {
			return
		}
	}
	if h.applyQueue != nil {
		_, _ = h.applyQueue.Enqueue(task, asynq.Queue("tunnel"), asynq.MaxRetry(3))
	}
}

func (h *ClusterRegistrationHandler) registrationRetryTask(clusterID, stepID uuid.UUID, stepName string) (*asynq.Task, string, int, string) {
	if stepName == "argocd_registration_failed" {
		task, err := tasks.NewArgoCDAutoRegisterClusterTask(clusterID)
		if err == nil {
			return task, "default", 5, fmt.Sprintf("cluster_registration:retry:argocd_auto_register:%s:%s", clusterID.String(), stepID.String())
		}
	}
	return asynq.NewTask("cluster_template:apply", mustRegistrationJSON(map[string]any{"cluster_id": clusterID.String()})),
		"tunnel",
		3,
		fmt.Sprintf("cluster_registration:retry:%s:%s", clusterID.String(), stepID.String())
}

func (h *ClusterRegistrationHandler) enqueueRegistrationRetryTask(ctx context.Context, task *asynq.Task, queueName string, maxRetry int, dedupeKey string) {
	if task == nil {
		return
	}
	if h.taskOutbox != nil {
		if _, err := tasks.EnqueueTaskOutbox(ctx, h.taskOutbox, task, tasks.TaskOutboxOptions{
			DedupeKey:           dedupeKey,
			QueueName:           queueName,
			MaxRetry:            maxRetry,
			MaxDeliveryAttempts: 20,
		}); err == nil {
			return
		}
	}
	switch task.Type() {
	case tasks.ArgoCDAutoRegisterClusterType:
		if h.argoCDAutoRegisterQueue != nil {
			_, _ = h.argoCDAutoRegisterQueue.Enqueue(task, asynq.Queue(queueName), asynq.MaxRetry(maxRetry))
		}
	default:
		if h.applyQueue != nil {
			_, _ = h.applyQueue.Enqueue(task, asynq.Queue(queueName), asynq.MaxRetry(maxRetry))
		}
	}
}

func (h *ClusterRegistrationHandler) updateRetryStepWithTaskOutbox(ctx context.Context, stepID uuid.UUID, task *asynq.Task, dedupeKey, queueName string, maxRetry int) (sqlc.ClusterRegistrationStep, bool, error) {
	atomicQ, ok := h.queries.(clusterRegistrationStepTaskOutboxQuerier)
	if !ok || h.taskOutbox == nil || task == nil {
		return sqlc.ClusterRegistrationStep{}, false, nil
	}
	step, err := atomicQ.UpdateClusterRegistrationStepWithTaskOutbox(ctx, sqlc.UpdateClusterRegistrationStepWithTaskOutboxParams{
		ID:                  stepID,
		Status:              "pending",
		ProgressPct:         0,
		DetailJSON:          nil,
		StartedAt:           pgtype.Timestamptz{},
		CompletedAt:         pgtype.Timestamptz{},
		ErrorMessage:        "",
		DedupeKey:           pgtype.Text{String: dedupeKey, Valid: true},
		TaskType:            task.Type(),
		Payload:             task.Payload(),
		QueueName:           queueName,
		MaxRetry:            int32(maxRetry),
		MaxDeliveryAttempts: 20,
		NextAttemptAt:       pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	return step, true, err
}

func (h *ClusterRegistrationHandler) publishRegistrationStep(step sqlc.ClusterRegistrationStep) {
	if h == nil || h.bus == nil {
		return
	}
	payload := map[string]any{
		"cluster_id": step.ClusterID,
		"step_id":    step.ID,
		"step_name":  step.StepName,
		"label":      step.Label,
		"status":     step.Status,
		"progress":   int(step.ProgressPct),
		"detail":     step.DetailJson,
		"error":      step.ErrorMessage,
		"step_order": int(step.StepOrder),
	}
	if step.StartedAt.Valid {
		payload["started_at"] = step.StartedAt.Time.UTC()
	}
	if step.CompletedAt.Valid {
		payload["completed_at"] = step.CompletedAt.Time.UTC()
	}
	h.bus.Publish(events.Type("cluster.registration.step"), payload)
}

func (h *ClusterRegistrationHandler) recordTemplateAttachFailure(ctx context.Context, clusterID uuid.UUID, cause error) {
	if h == nil || h.service == nil {
		return
	}
	_, _ = h.service.WriteStep(ctx, clusterID, registration.StepInput{
		StepName:      "template_failed",
		Status:        "failed",
		ProgressPct:   0,
		ErrorMessage:  cause.Error(),
		MarkCompleted: true,
	})
}

func mustRegistrationJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
