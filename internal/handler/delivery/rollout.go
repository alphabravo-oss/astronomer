package delivery

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	deliveryrollout "github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type RolloutQueries interface {
	ListDeliveryRollouts(context.Context, sqlc.ListDeliveryRolloutsParams) ([]sqlc.DeliveryRollout, error)
	CountDeliveryRollouts(context.Context, sqlc.CountDeliveryRolloutsParams) (int64, error)
	GetDeliveryRollout(context.Context, sqlc.GetDeliveryRolloutParams) (sqlc.DeliveryRollout, error)
	ListDeliveryRolloutClusters(context.Context, sqlc.ListDeliveryRolloutClustersParams) ([]sqlc.DeliveryRolloutCluster, error)
	CountDeliveryRolloutClusters(context.Context, sqlc.CountDeliveryRolloutClustersParams) (int64, error)
	ListDeliveryRolloutEvents(context.Context, sqlc.ListDeliveryRolloutEventsParams) ([]sqlc.DeliveryRolloutEvent, error)
	CountDeliveryRolloutEvents(context.Context, sqlc.CountDeliveryRolloutEventsParams) (int64, error)
	ListDeliveryRolloutApprovals(context.Context, uuid.UUID) ([]sqlc.DeliveryRolloutApproval, error)
}

type RolloutPlanner interface {
	Create(context.Context, deliveryrollout.CreateRequest) (deliveryrollout.FrozenRollout, error)
}

type RolloutHandler struct {
	queries    RolloutQueries
	planner    RolloutPlanner
	controller deliveryrollout.Controller
	bus        *events.Bus
}

func NewRolloutHandler(queries RolloutQueries, planner RolloutPlanner, controller deliveryrollout.Controller, bus *events.Bus) *RolloutHandler {
	return &RolloutHandler{queries: queries, planner: planner, controller: controller, bus: bus}
}

// openapi:request DeliveryRolloutStart
type startRolloutRequest struct {
	ProjectID          uuid.UUID             `json:"project_id,omitempty"`
	PreviewDigest      model.Digest          `json:"preview_digest"`
	ConfirmAllClusters bool                  `json:"confirm_all_clusters"`
	Strategy           model.RolloutStrategy `json:"strategy"`
}

// openapi:request DeliveryRolloutAction
type rolloutActionRequest struct {
	ProjectID  uuid.UUID `json:"project_id,omitempty"`
	ReasonCode string    `json:"reason_code,omitempty"`
}

// openapi:request DeliveryRolloutApproval
type rolloutApprovalRequest struct {
	ProjectID     uuid.UUID    `json:"project_id,omitempty"`
	Cohort        int32        `json:"cohort"`
	BindingDigest model.Digest `json:"binding_digest"`
	Decision      string       `json:"decision"`
	ExpiresAt     time.Time    `json:"expires_at"`
}

type rolloutSummary struct {
	ID                  uuid.UUID  `json:"id"`
	TargetID            uuid.UUID  `json:"target_id"`
	TargetGeneration    int64      `json:"target_generation"`
	FromBundleVersionID *uuid.UUID `json:"from_bundle_version_id,omitempty"`
	ToBundleVersionID   uuid.UUID  `json:"to_bundle_version_id"`
	PlacementDigest     string     `json:"placement_digest"`
	Strategy            any        `json:"strategy"`
	PlanDigest          string     `json:"plan_digest"`
	State               string     `json:"state"`
	FencingGeneration   int64      `json:"fencing_generation"`
	TotalClusters       int32      `json:"total_clusters"`
	ReadyClusters       int32      `json:"ready_clusters"`
	FailedClusters      int32      `json:"failed_clusters"`
	BlockedClusters     int32      `json:"blocked_clusters"`
	ReleasedClusters    int32      `json:"released_clusters"`
	ProgressDeadline    *time.Time `json:"progress_deadline,omitempty"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
	LastErrorCode       string     `json:"last_error_code,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

func (h *RolloutHandler) Start(w http.ResponseWriter, r *http.Request) {
	expected, err := requireIfMatch(r)
	if err != nil || expected < 1 {
		respondError(w, http.StatusPreconditionRequired, "if_match_required", "If-Match must contain the current positive target generation")
		return
	}
	key, err := requiredIdempotencyKey(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request startRolloutRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, request.ProjectID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	targetID, err := pathUUID(r, "id", "targetID")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.planner == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery rollout planner is unavailable")
		return
	}
	actor := rolloutActor(r)
	plan, err := h.planner.Create(r.Context(), deliveryrollout.CreateRequest{
		TargetID: targetID, ExpectedTargetGeneration: uint64(expected), PreviewDigest: request.PreviewDigest,
		ConfirmAllClusters: request.ConfirmAllClusters, Strategy: request.Strategy,
		Actor: actor, IdempotencyKey: key,
	})
	if err != nil {
		respondRolloutError(w, err)
		return
	}
	if plan.ProjectID != projectID {
		respondError(w, http.StatusNotFound, "not_found", "delivery resource not found")
		return
	}
	events.PublishChanged(h.bus, "delivery_rollout", "", plan.ID.String(), map[string]any{"project_id": projectID.String(), "action": "created"})
	recordAudit(r, h.queries, "delivery.rollout.created", "delivery_rollout", plan.ID.String(), "", map[string]any{
		"project_id":        projectID.String(),
		"target_id":         plan.TargetID.String(),
		"target_generation": plan.TargetGeneration,
		"placement_digest":  plan.PlacementDigest.String(),
		"plan_digest":       plan.PlanDigest.String(),
		"cluster_count":     len(plan.Clusters),
		"approval_required": plan.Approval.Required,
	})
	respondData(w, http.StatusAccepted, plan)
}

func (h *RolloutHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, err := projectIDFromRequest(r, uuid.Nil)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	limit, offset, err := parsePagination(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	state, err := rolloutStateFilter(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_filter", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery rollout persistence is unavailable")
		return
	}
	params := sqlc.ListDeliveryRolloutsParams{ProjectID: projectID, State: state, QueryLimit: limit, QueryOffset: offset}
	rows, err := h.queries.ListDeliveryRollouts(r.Context(), params)
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	total, err := h.queries.CountDeliveryRollouts(r.Context(), sqlc.CountDeliveryRolloutsParams{ProjectID: projectID, State: state})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	items := make([]rolloutSummary, 0, len(rows))
	for _, row := range rows {
		item, err := rolloutFromRow(row)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored rollout is invalid")
			return
		}
		items = append(items, item)
	}
	respondPage(w, r, items, total, limit, offset, int64(offset)+int64(len(items)) < total, true)
}

func (h *RolloutHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID, rolloutID, ok := rolloutScope(w, r)
	if !ok {
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery rollout persistence is unavailable")
		return
	}
	row, err := h.queries.GetDeliveryRollout(r.Context(), sqlc.GetDeliveryRolloutParams{ID: rolloutID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	summary, err := rolloutFromRow(row)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored rollout is invalid")
		return
	}
	approvals, err := h.queries.ListDeliveryRolloutApprovals(r.Context(), rolloutID)
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	eventsRows, err := h.queries.ListDeliveryRolloutEvents(r.Context(), sqlc.ListDeliveryRolloutEventsParams{RolloutID: rolloutID, ProjectID: projectID, QueryLimit: 50, QueryOffset: 0})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	setEntityTag(w, row.FencingGeneration)
	respondData(w, http.StatusOK, map[string]any{
		"rollout": summary, "frozen_plan": row.FrozenPlan, "approvals": approvals, "timeline": eventsRows,
	})
}

func (h *RolloutHandler) Clusters(w http.ResponseWriter, r *http.Request) {
	projectID, rolloutID, ok := rolloutScope(w, r)
	if !ok {
		return
	}
	limit, offset, err := parsePagination(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	rows, err := h.queries.ListDeliveryRolloutClusters(r.Context(), sqlc.ListDeliveryRolloutClustersParams{RolloutID: rolloutID, ProjectID: projectID, QueryLimit: limit, QueryOffset: offset})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	total, err := h.queries.CountDeliveryRolloutClusters(r.Context(), sqlc.CountDeliveryRolloutClustersParams{RolloutID: rolloutID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	respondPage(w, r, rows, total, limit, offset, int64(offset)+int64(len(rows)) < total, true)
}

func (h *RolloutHandler) Events(w http.ResponseWriter, r *http.Request) {
	projectID, rolloutID, ok := rolloutScope(w, r)
	if !ok {
		return
	}
	limit, offset, err := parsePagination(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	rows, err := h.queries.ListDeliveryRolloutEvents(r.Context(), sqlc.ListDeliveryRolloutEventsParams{RolloutID: rolloutID, ProjectID: projectID, QueryLimit: limit, QueryOffset: offset})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	total, err := h.queries.CountDeliveryRolloutEvents(r.Context(), sqlc.CountDeliveryRolloutEventsParams{RolloutID: rolloutID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	respondPage(w, r, rows, total, limit, offset, int64(offset)+int64(len(rows)) < total, true)
}

func (h *RolloutHandler) Pause(w http.ResponseWriter, r *http.Request) {
	h.action(w, r, deliveryrollout.ActionPause)
}
func (h *RolloutHandler) Resume(w http.ResponseWriter, r *http.Request) {
	h.action(w, r, deliveryrollout.ActionResume)
}
func (h *RolloutHandler) Abort(w http.ResponseWriter, r *http.Request) {
	h.action(w, r, deliveryrollout.ActionAbort)
}
func (h *RolloutHandler) Retry(w http.ResponseWriter, r *http.Request) {
	h.action(w, r, deliveryrollout.ActionRetry)
}
func (h *RolloutHandler) Rollback(w http.ResponseWriter, r *http.Request) {
	h.action(w, r, deliveryrollout.ActionRollback)
}

func (h *RolloutHandler) action(w http.ResponseWriter, r *http.Request, action deliveryrollout.Action) {
	expected, err := requireIfMatch(r)
	if err != nil || expected < 1 {
		respondError(w, http.StatusPreconditionRequired, "if_match_required", "If-Match must contain the current positive rollout fencing generation")
		return
	}
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request rolloutActionRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, request.ProjectID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	rolloutID, err := pathUUID(r, "id", "rolloutID")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.controller == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery rollout controls are unavailable")
		return
	}
	result, err := h.controller.Act(r.Context(), deliveryrollout.ActionRequest{
		ProjectID: projectID, RolloutID: rolloutID, ExpectedFence: expected, Action: action,
		ActorID: middleware.AuthenticatedUserUUID(r.Context()), ReasonCode: request.ReasonCode,
	})
	if err != nil {
		respondRolloutError(w, err)
		return
	}
	setEntityTag(w, result.Rollout.FencingGeneration)
	events.PublishChanged(h.bus, "delivery_rollout", "", rolloutID.String(), map[string]any{"project_id": projectID.String(), "action": string(action)})
	recordAudit(r, h.queries, rolloutAuditAction(action), "delivery_rollout", rolloutID.String(), "", map[string]any{
		"project_id":         projectID.String(),
		"target_id":          result.Rollout.TargetID.String(),
		"state":              result.Rollout.State,
		"fencing_generation": result.Rollout.FencingGeneration,
	})
	respondData(w, http.StatusAccepted, map[string]any{"rollout": result.Rollout, "event": result.Event})
}

func (h *RolloutHandler) Approve(w http.ResponseWriter, r *http.Request) {
	expected, err := requireIfMatch(r)
	if err != nil || expected < 1 {
		respondError(w, http.StatusPreconditionRequired, "if_match_required", "If-Match must contain the current positive rollout fencing generation")
		return
	}
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request rolloutApprovalRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, request.ProjectID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	rolloutID, err := pathUUID(r, "id", "rolloutID")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.controller == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery rollout controls are unavailable")
		return
	}
	result, err := h.controller.Approve(r.Context(), deliveryrollout.ApprovalRequest{
		ProjectID: projectID, RolloutID: rolloutID, ExpectedFence: expected, Cohort: request.Cohort,
		BindingDigest: request.BindingDigest, Decision: request.Decision,
		ActorID: middleware.AuthenticatedUserUUID(r.Context()), ExpiresAt: request.ExpiresAt,
	})
	if err != nil {
		respondRolloutError(w, err)
		return
	}
	setEntityTag(w, result.Rollout.FencingGeneration)
	events.PublishChanged(h.bus, "delivery_rollout", "", rolloutID.String(), map[string]any{"project_id": projectID.String(), "action": request.Decision, "cohort": request.Cohort})
	recordAudit(r, h.queries, "delivery.rollout.approval_recorded", "delivery_rollout", rolloutID.String(), "", map[string]any{
		"project_id":         projectID.String(),
		"target_id":          result.Rollout.TargetID.String(),
		"decision":           request.Decision,
		"cohort":             request.Cohort,
		"binding_digest":     request.BindingDigest.String(),
		"fencing_generation": result.Rollout.FencingGeneration,
	})
	respondData(w, http.StatusAccepted, map[string]any{"rollout": result.Rollout, "approval": result.Approval, "event": result.Event})
}

func rolloutAuditAction(action deliveryrollout.Action) string {
	switch action {
	case deliveryrollout.ActionPause:
		return "delivery.rollout.paused"
	case deliveryrollout.ActionResume:
		return "delivery.rollout.resumed"
	case deliveryrollout.ActionAbort:
		return "delivery.rollout.aborted"
	case deliveryrollout.ActionRetry:
		return "delivery.rollout.retry_requested"
	case deliveryrollout.ActionRollback:
		return "delivery.rollout.rollback_requested"
	default:
		return "delivery.rollout.action_requested"
	}
}

func rolloutFromRow(row sqlc.DeliveryRollout) (rolloutSummary, error) {
	var strategy model.RolloutStrategy
	if err := decodeStrictJSON(row.Strategy, &strategy); err != nil {
		return rolloutSummary{}, err
	}
	var previous *uuid.UUID
	if row.FromBundleVersionID.Valid {
		value := uuid.UUID(row.FromBundleVersionID.Bytes)
		previous = &value
	}
	return rolloutSummary{
		ID: row.ID, TargetID: row.TargetID, TargetGeneration: row.TargetGeneration,
		FromBundleVersionID: previous, ToBundleVersionID: row.ToBundleVersionID,
		PlacementDigest: row.PlacementDigest, Strategy: strategy, PlanDigest: row.PlanDigest,
		State: row.State, FencingGeneration: row.FencingGeneration,
		TotalClusters: row.TotalClusters, ReadyClusters: row.ReadyClusters,
		FailedClusters: row.FailedClusters, BlockedClusters: row.BlockedClusters,
		ReleasedClusters: row.ReleasedClusters, ProgressDeadline: timestampPointer(row.ProgressDeadline),
		StartedAt: timestampPointer(row.StartedAt), CompletedAt: timestampPointer(row.CompletedAt),
		LastErrorCode: row.LastErrorCode, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

func rolloutScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	projectID, err := projectIDFromRequest(r, uuid.Nil)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return uuid.Nil, uuid.Nil, false
	}
	rolloutID, err := pathUUID(r, "id", "rolloutID")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, rolloutID, true
}

func rolloutStateFilter(r *http.Request) (pgtype.Text, error) {
	values, ok := r.URL.Query()["state"]
	if !ok {
		return pgtype.Text{}, nil
	}
	if len(values) != 1 {
		return pgtype.Text{}, errors.New("state must occur exactly once")
	}
	state := model.RolloutState(values[0])
	if !state.Valid() {
		return pgtype.Text{}, errors.New("state is not a supported rollout state")
	}
	return pgtype.Text{String: string(state), Valid: true}, nil
}

func requiredIdempotencyKey(r *http.Request) (string, error) {
	if err := validateIdempotencyKey(r); err != nil {
		return "", err
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return "", errors.New("Idempotency-Key is required")
	}
	return key, nil
}

func rolloutActor(r *http.Request) string {
	if user, ok := middleware.GetAuthenticatedUser(r.Context()); ok && user != nil {
		if id := strings.TrimSpace(user.ID); id != "" {
			return id
		}
	}
	return "system"
}

func timestampPointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	timestamp := value.Time.UTC()
	return &timestamp
}
