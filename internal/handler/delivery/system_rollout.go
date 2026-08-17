package delivery

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/systemrollout"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type SystemRolloutService interface {
	Start(context.Context, systemrollout.StartRequest) (systemrollout.View, error)
	Get(context.Context, uuid.UUID) (systemrollout.View, error)
	Assignments(context.Context, uuid.UUID) ([]systemrollout.Assignment, error)
	Act(context.Context, uuid.UUID, int64, systemrollout.Action, uuid.UUID, string) (systemrollout.View, error)
}

type SystemRolloutHandler struct {
	service SystemRolloutService
	audit   any
	bus     *events.Bus
}

func NewSystemRolloutHandler(service SystemRolloutService, auditWriter any, bus *events.Bus) *SystemRolloutHandler {
	return &SystemRolloutHandler{service: service, audit: auditWriter, bus: bus}
}

type systemRolloutStartRequest struct {
	ReleaseID uuid.UUID             `json:"release_id"`
	Strategy  model.RolloutStrategy `json:"strategy"`
}

type systemRolloutActionRequest struct {
	ReasonCode string `json:"reason_code,omitempty"`
}

func (h *SystemRolloutHandler) Start(w http.ResponseWriter, r *http.Request) {
	key, err := requiredIdempotencyKey(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request systemRolloutStartRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if h == nil || h.service == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery system rollout service is unavailable")
		return
	}
	result, err := h.service.Start(r.Context(), systemrollout.StartRequest{
		ReleaseID: request.ReleaseID, Strategy: request.Strategy, IdempotencyKey: key,
		ActorID: authenticatedActorUUID(r),
	})
	if err != nil {
		respondSystemRolloutError(w, err)
		return
	}
	setEntityTag(w, result.FencingGeneration)
	events.PublishChanged(h.bus, "delivery_system_rollout", "", result.ID.String(), map[string]any{"action": "created"})
	recordAudit(r, h.audit, "delivery.system_rollout.created", "delivery_system_rollout", result.ID.String(), "", map[string]any{
		"release_id": result.ReleaseID.String(), "strategy_digest": result.StrategyDigest,
		"cluster_count": result.TotalClusters, "state": result.State,
	})
	respondData(w, http.StatusAccepted, result)
}

func (h *SystemRolloutHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "id", "rolloutID")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.service == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery system rollout service is unavailable")
		return
	}
	result, err := h.service.Get(r.Context(), id)
	if err != nil {
		respondSystemRolloutError(w, err)
		return
	}
	setEntityTag(w, result.FencingGeneration)
	respondData(w, http.StatusOK, result)
}

func (h *SystemRolloutHandler) Assignments(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "id", "rolloutID")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.service == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery system rollout service is unavailable")
		return
	}
	items, err := h.service.Assignments(r.Context(), id)
	if err != nil {
		respondSystemRolloutError(w, err)
		return
	}
	respondData(w, http.StatusOK, items)
}

func (h *SystemRolloutHandler) Approve(w http.ResponseWriter, r *http.Request) {
	h.action(w, r, systemrollout.ActionApprove)
}

func (h *SystemRolloutHandler) Pause(w http.ResponseWriter, r *http.Request) {
	h.action(w, r, systemrollout.ActionPause)
}

func (h *SystemRolloutHandler) Resume(w http.ResponseWriter, r *http.Request) {
	h.action(w, r, systemrollout.ActionResume)
}

func (h *SystemRolloutHandler) Abort(w http.ResponseWriter, r *http.Request) {
	h.action(w, r, systemrollout.ActionAbort)
}

func (h *SystemRolloutHandler) Retry(w http.ResponseWriter, r *http.Request) {
	h.action(w, r, systemrollout.ActionRetry)
}

func (h *SystemRolloutHandler) Rollback(w http.ResponseWriter, r *http.Request) {
	h.action(w, r, systemrollout.ActionRollback)
}

func (h *SystemRolloutHandler) action(w http.ResponseWriter, r *http.Request, action systemrollout.Action) {
	expected, err := requireIfMatch(r)
	if err != nil || expected < 1 {
		respondError(w, http.StatusPreconditionRequired, "if_match_required", "If-Match must contain the current positive system rollout fencing generation")
		return
	}
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request systemRolloutActionRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	id, err := pathUUID(r, "id", "rolloutID")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.service == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery system rollout service is unavailable")
		return
	}
	result, err := h.service.Act(r.Context(), id, expected, action, authenticatedActorUUID(r), strings.TrimSpace(request.ReasonCode))
	if err != nil {
		respondSystemRolloutError(w, err)
		return
	}
	setEntityTag(w, result.FencingGeneration)
	events.PublishChanged(h.bus, "delivery_system_rollout", "", result.ID.String(), map[string]any{"action": string(action)})
	recordAudit(r, h.audit, "delivery.system_rollout."+string(action), "delivery_system_rollout", result.ID.String(), "", map[string]any{
		"release_id": result.ReleaseID.String(), "strategy_digest": result.StrategyDigest,
		"state": result.State, "fencing_generation": result.FencingGeneration,
		"reason_code": strings.TrimSpace(request.ReasonCode),
	})
	respondData(w, http.StatusAccepted, result)
}

func authenticatedActorUUID(r *http.Request) uuid.UUID {
	if r == nil {
		return uuid.Nil
	}
	actor := middleware.AuthenticatedUserUUID(r.Context())
	if !actor.Valid {
		return uuid.Nil
	}
	return uuid.UUID(actor.Bytes)
}

func respondSystemRolloutError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, systemrollout.ErrNotFound):
		respondError(w, http.StatusNotFound, "not_found", "delivery system rollout or release not found")
	case errors.Is(err, systemrollout.ErrConflict):
		respondError(w, http.StatusConflict, "conflict", "delivery system rollout changed or another rollout is active")
	case errors.Is(err, systemrollout.ErrPrecondition):
		respondError(w, http.StatusPreconditionFailed, "precondition_failed", safeSystemError(err))
	case errors.Is(err, systemrollout.ErrInvalidTransition):
		respondError(w, http.StatusConflict, "invalid_transition", safeSystemError(err))
	default:
		respondError(w, http.StatusInternalServerError, "internal_error", "delivery system rollout operation failed")
	}
}

func safeSystemError(err error) string {
	message := err.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}
