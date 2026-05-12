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

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// ClusterRegistrationQuerier is the small DB surface the handler
// needs. Local interface so the wiring layer can inject *sqlc.Queries
// directly and the handler tests can supply a fake.
type ClusterRegistrationQuerier interface {
	registration.Querier
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	// Auto-attach for install_baseline=true at confirm time.
	UpsertClusterTemplateApplication(ctx context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error)
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
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	status, err := h.service.LoadStatus(r.Context(), id)
	if err != nil {
		if err == pgx.ErrNoRows {
			RespondError(w, http.StatusNotFound, "not_found", "Cluster not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, "load_error", "Failed to load registration status")
		return
	}
	RespondJSON(w, http.StatusOK, status)
}

// PutOptions handles PUT /clusters/{id}/registration/options/.
// Body: {"install_baseline": bool}
func (h *ClusterRegistrationHandler) PutOptions(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	var req struct {
		InstallBaseline *bool `json:"install_baseline"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if req.InstallBaseline == nil {
		RespondError(w, http.StatusBadRequest, "validation_error", "install_baseline is required")
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}
	if _, err := h.service.SetInstallBaseline(r.Context(), id, *req.InstallBaseline); err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to record options")
		return
	}
	recordAudit(r, h.auditQueries, "cluster.registration.options", "cluster", id.String(), "", map[string]any{
		"install_baseline": *req.InstallBaseline,
	})
	status, err := h.service.LoadStatus(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "load_error", "Failed to load status")
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
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}
	record, advErr := h.service.Advance(r.Context(), id, registration.EventConfirm)
	if advErr != nil {
		if h.isIllegal(advErr) {
			RespondError(w, http.StatusConflict, "illegal_transition", advErr.Error())
			return
		}
		RespondError(w, http.StatusInternalServerError, "transition_error", advErr.Error())
		return
	}

	if record.InstallBaseline.Valid && record.InstallBaseline.Bool && h.baselineTemplateID != uuid.Nil {
		// Auto-attach the platform-baseline template. Spec snapshot
		// is empty here; the cluster_templates row contains the
		// canonical spec and the apply worker reads it via the
		// template_id FK.
		if _, err := h.queries.UpsertClusterTemplateApplication(r.Context(), sqlc.UpsertClusterTemplateApplicationParams{
			ClusterID:    id,
			TemplateID:   h.baselineTemplateID,
			SpecSnapshot: json.RawMessage(`{}`),
		}); err != nil {
			RespondError(w, http.StatusInternalServerError, "attach_error", "Failed to attach platform baseline")
			return
		}
		// Enqueue the apply task. Best-effort: when the queue isn't
		// wired the periodic sweep picks the row up.
		if h.applyQueue != nil {
			task := asynq.NewTask("cluster_template:apply",
				mustRegistrationJSON(map[string]any{"cluster_id": id.String()}))
			_, _ = h.applyQueue.Enqueue(task)
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
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	stepID, err := uuid.Parse(chi.URLParam(r, "step_id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_step", "Invalid step ID")
		return
	}
	step, err := h.queries.GetClusterRegistrationStep(r.Context(), stepID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Step not found")
		return
	}
	if step.ClusterID != id {
		RespondError(w, http.StatusBadRequest, "step_mismatch", "Step does not belong to cluster")
		return
	}
	if step.Status != "failed" {
		RespondError(w, http.StatusConflict, "not_failed", "Only failed steps can be retried")
		return
	}
	// Advance back to provisioning so the timeline doesn't lie about
	// being stuck at failed while the task is re-running.
	if _, err := h.service.Advance(r.Context(), id, registration.EventRetry); err != nil {
		RespondError(w, http.StatusConflict, "illegal_transition", err.Error())
		return
	}
	// Re-fire the underlying task. We only know how to retry the
	// cluster_template apply at this layer; tool-specific retries
	// just bounce the apply task — it's idempotent.
	if h.applyQueue != nil {
		task := asynq.NewTask("cluster_template:apply",
			mustRegistrationJSON(map[string]any{"cluster_id": id.String()}))
		_, _ = h.applyQueue.Enqueue(task)
	}
	// Mark the failing step as `pending` again so the UI shows it
	// as queued rather than the error remaining sticky.
	_, _ = h.service.UpdateStep(r.Context(), registration.UpdateStepInput{
		StepID:      stepID,
		Status:      "pending",
		ProgressPct: 0,
	})
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
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	caller, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || caller == nil {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	callerID, err := uuid.Parse(caller.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid caller ID")
		return
	}
	user, err := h.queries.GetUserByID(r.Context(), callerID)
	if err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", "Caller not found")
		return
	}
	if !user.IsSuperuser {
		RespondError(w, http.StatusForbidden, "forbidden", "Cancel requires superuser")
		return
	}
	if _, err := h.service.Advance(r.Context(), id, registration.EventCancel,
		registration.WithError("cancelled by superuser")); err != nil {
		if h.isIllegal(err) {
			RespondError(w, http.StatusConflict, "illegal_transition", err.Error())
			return
		}
		RespondError(w, http.StatusInternalServerError, "transition_error", err.Error())
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
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
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
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	ch := h.bus.Subscribe(r.Context())
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": ping\n\n")
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
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, payload)
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

func mustRegistrationJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
