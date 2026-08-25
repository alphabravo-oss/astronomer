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
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
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

type ClusterRegistrationMutationTx interface {
	ClusterRegistrationQuerier
	audit.OutboxQuerier
}

type clusterRegistrationRunTxFunc func(context.Context, func(ClusterRegistrationMutationTx) error) error

func executeClusterRegistrationMutation[T any](r *http.Request, h *ClusterRegistrationHandler, mutate func(ClusterRegistrationQuerier, *registration.Service) (T, error), describe func(T) clusterAuditEvent) (T, error) {
	var zero T
	if h.runTx != nil {
		var result T
		flush := func() {}
		err := h.runTx(r.Context(), func(q ClusterRegistrationMutationTx) error {
			service, flushEffects := h.service.Buffered(q)
			flush = flushEffects
			var mutationErr error
			result, mutationErr = mutate(q, service)
			if mutationErr != nil {
				return mutationErr
			}
			event := describe(result)
			return recordAuditOutbox(r, q, event.action, event.resourceType, event.resourceID, event.resourceName, event.status, event.detail)
		})
		if err == nil {
			flush()
		}
		return result, err
	}
	result, err := mutate(h.queries, h.service)
	if err != nil {
		return zero, err
	}
	event := describe(result)
	recordAudit(r, h.auditQueries, event.action, event.resourceType, event.resourceID, event.resourceName, event.detail)
	return result, nil
}

// ClusterRegistrationHandler bundles the wizard endpoints.
type ClusterRegistrationHandler struct {
	queries ClusterRegistrationQuerier
	service *registration.Service
	bus     *events.Bus
	runTx   clusterRegistrationRunTxFunc
	// auditQueries lets recordAudit write through. Same querier
	// works for both since *sqlc.Queries implements the broader
	// audit surface, so we just pass the same interface.
	auditQueries any
}

type clusterRegistrationMutationResult struct {
	cluster sqlc.Cluster
	record  sqlc.ClusterRegistrationRecord
	step    sqlc.ClusterRegistrationStep
}

var (
	errRegistrationStepMismatch  = errors.New("registration step belongs to another cluster")
	errRegistrationStepNotFailed = errors.New("registration step is not failed")
)

func getClusterRegistrationStepForMutation(ctx context.Context, q ClusterRegistrationQuerier, id uuid.UUID) (sqlc.ClusterRegistrationStep, error) {
	if locker, ok := q.(interface {
		GetClusterRegistrationStepForUpdate(context.Context, uuid.UUID) (sqlc.ClusterRegistrationStep, error)
	}); ok {
		return locker.GetClusterRegistrationStepForUpdate(ctx, id)
	}
	return q.GetClusterRegistrationStep(ctx, id)
}

func (h *ClusterRegistrationHandler) SetRunTx(runTx clusterRegistrationRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *ClusterRegistrationHandler) TransactionalAuditWired() bool {
	return h != nil && h.runTx != nil
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
	_, err = executeClusterRegistrationMutation(r, h,
		func(q ClusterRegistrationQuerier, service *registration.Service) (clusterRegistrationMutationResult, error) {
			cluster, getErr := q.GetClusterByID(r.Context(), id)
			if getErr != nil {
				return clusterRegistrationMutationResult{}, getErr
			}
			record, setErr := service.SetInstallBaseline(r.Context(), id, *req.InstallBaseline)
			return clusterRegistrationMutationResult{cluster: cluster, record: record}, setErr
		},
		func(result clusterRegistrationMutationResult) clusterAuditEvent {
			return clusterAuditEvent{
				action: "cluster.registration.options", resourceType: "cluster", resourceID: id.String(), resourceName: result.cluster.Name, status: http.StatusOK,
				detail: map[string]any{"install_baseline": result.record.InstallBaseline.Valid && result.record.InstallBaseline.Bool},
			}
		},
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to record options")
		return
	}
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
	_, advErr := executeClusterRegistrationMutation(r, h,
		func(q ClusterRegistrationQuerier, service *registration.Service) (clusterRegistrationMutationResult, error) {
			cluster, getErr := q.GetClusterByID(r.Context(), id)
			if getErr != nil {
				return clusterRegistrationMutationResult{}, getErr
			}
			record, advanceErr := service.Advance(r.Context(), id, registration.EventConfirm)
			return clusterRegistrationMutationResult{cluster: cluster, record: record}, advanceErr
		},
		func(result clusterRegistrationMutationResult) clusterAuditEvent {
			return clusterAuditEvent{
				action: "cluster.registration.confirm", resourceType: "cluster", resourceID: id.String(), resourceName: result.cluster.Name, status: http.StatusOK,
				detail: map[string]any{"install_baseline": result.record.InstallBaseline.Valid && result.record.InstallBaseline.Bool},
			}
		},
	)
	if advErr != nil {
		if errors.Is(advErr, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
			return
		}
		if h.isIllegal(advErr) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, advErr.Error())
			return
		}
		respondTransactionalMutationError(w, r, advErr, http.StatusInternalServerError, apierror.TransitionError, "Failed to confirm registration")
		return
	}
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
	_, err = executeClusterRegistrationMutation(r, h,
		func(q ClusterRegistrationQuerier, service *registration.Service) (clusterRegistrationMutationResult, error) {
			step, getErr := getClusterRegistrationStepForMutation(r.Context(), q, stepID)
			if getErr != nil {
				return clusterRegistrationMutationResult{}, getErr
			}
			if step.ClusterID != id {
				return clusterRegistrationMutationResult{}, errRegistrationStepMismatch
			}
			if step.Status != "failed" {
				return clusterRegistrationMutationResult{}, errRegistrationStepNotFailed
			}
			record, advanceErr := service.Advance(r.Context(), id, registration.EventRetry)
			if advanceErr != nil {
				return clusterRegistrationMutationResult{}, advanceErr
			}
			if _, writeErr := service.WriteStep(r.Context(), id, registration.StepInput{
				StepName: "delivery_retry_requested", Status: "success", Detail: map[string]any{"failed_step_id": stepID.String()},
			}); writeErr != nil {
				return clusterRegistrationMutationResult{}, writeErr
			}
			return clusterRegistrationMutationResult{record: record, step: step}, nil
		},
		func(result clusterRegistrationMutationResult) clusterAuditEvent {
			return clusterAuditEvent{
				action: "cluster.registration.retry", resourceType: "cluster", resourceID: id.String(), status: http.StatusOK,
				detail: map[string]any{"step_id": result.step.ID.String(), "step_name": result.step.StepName},
			}
		},
	)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Step not found")
		case errors.Is(err, errRegistrationStepMismatch):
			RespondRequestError(w, r, http.StatusBadRequest, apierror.StepMismatch, "Step does not belong to cluster")
		case errors.Is(err, errRegistrationStepNotFailed):
			RespondRequestError(w, r, http.StatusConflict, apierror.NotFailed, "Only failed steps can be retried")
		case h.isIllegal(err):
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, err.Error())
		default:
			respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.TransitionError, "Failed to retry registration")
		}
		return
	}
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
	_, err = executeClusterRegistrationMutation(r, h,
		func(_ ClusterRegistrationQuerier, service *registration.Service) (clusterRegistrationMutationResult, error) {
			record, advanceErr := service.Advance(r.Context(), id, registration.EventCancel, registration.WithError("cancelled by superuser"))
			return clusterRegistrationMutationResult{record: record}, advanceErr
		},
		func(clusterRegistrationMutationResult) clusterAuditEvent {
			return clusterAuditEvent{action: "cluster.registration.cancel", resourceType: "cluster", resourceID: id.String(), status: http.StatusOK}
		},
	)
	if err != nil {
		if h.isIllegal(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, err.Error())
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.TransitionError, "Failed to cancel registration")
		return
	}
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
