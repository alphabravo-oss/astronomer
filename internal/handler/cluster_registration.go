// Cluster-registration wizard endpoints (sprint 22 / migration 078).
//
// Six routes mount under /api/v1/clusters/{id}/registration/*:
//
//	GET   .../status/        — full Status (phase + steps)
//	GET   .../events/        — SSE stream of cluster.registration.* events
//	PUT   .../options/       — operator's step-1 install_baseline choice
//	POST  .../confirm/       — operator clicked "I've run it" on wizard page 2
//	POST  .../retry/{step}/  — request a fresh delivery rollout after failure
//	POST  .../cancel/        — superuser abort
//
// The phase machine itself lives in internal/registration/. This file
// is the HTTP shim — argument parsing, response shaping, audit.
package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
)

// ClusterRegistrationQuerier is the small DB surface the handler
// needs. Local interface so the wiring layer can inject *sqlc.Queries
// directly and the handler tests can supply a fake.
type ClusterRegistrationQuerier interface {
	registration.Querier
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

// ClusterRegistrationHandler bundles the wizard endpoints.
type ClusterRegistrationHandler struct {
	queries ClusterRegistrationQuerier
	service *registration.Service
	bus     *events.Bus
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

// Service exposes the underlying registration.Service to the tunnel hub and
// built-in delivery provisioner.
func (h *ClusterRegistrationHandler) Service() *registration.Service {
	if h == nil {
		return nil
	}
	return h.service
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
	// openapi:request SetOptionsRequest
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
// Advances the cluster from `created` to `awaiting_agent`. Baseline delivery
// is deliberately not started here: the authenticated agent must first report
// a Ready, compatible Flux inventory.
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
	recordAudit(r, h.auditQueries, "cluster.registration.confirm", "cluster", id.String(), cluster.Name, map[string]any{
		"install_baseline": record.InstallBaseline.Valid && record.InstallBaseline.Bool,
	})

	status, _ := h.service.LoadStatus(r.Context(), id)
	RespondJSON(w, http.StatusOK, status)
}

// PostRetry handles POST /clusters/{id}/registration/retry/{step_id}/.
// Records an explicit retry request. The next authenticated Ready inventory
// observation creates a new idempotent delivery rollout; there is no hidden
// cluster-template or Helm task path.
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
	_, _ = h.service.WriteStep(r.Context(), id, registration.StepInput{
		StepName: "delivery_retry_requested",
		Status:   "success",
		Detail:   map[string]any{"failed_step_id": stepID.String()},
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
