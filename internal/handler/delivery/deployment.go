package delivery

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	deliverydeployment "github.com/alphabravocompany/astronomer-go/internal/delivery/deployment"
	"github.com/alphabravocompany/astronomer-go/internal/events"
)

type DeploymentQueries interface {
	ListClusterDeployments(context.Context, sqlc.ListClusterDeploymentsParams) ([]sqlc.ClusterDeployment, error)
	CountClusterDeployments(context.Context, sqlc.CountClusterDeploymentsParams) (int64, error)
	GetClusterDeployment(context.Context, sqlc.GetClusterDeploymentParams) (sqlc.ClusterDeployment, error)
	ListClusterDeploymentEvents(context.Context, sqlc.ListClusterDeploymentEventsParams) ([]sqlc.ClusterDeploymentEvent, error)
	CountClusterDeploymentEvents(context.Context, sqlc.CountClusterDeploymentEventsParams) (int64, error)
}

type DeploymentHandler struct {
	queries    DeploymentQueries
	controller deliverydeployment.Controller
	bus        *events.Bus
}

func NewDeploymentHandler(queries DeploymentQueries, controller deliverydeployment.Controller, bus *events.Bus) *DeploymentHandler {
	return &DeploymentHandler{queries: queries, controller: controller, bus: bus}
}

type deploymentActionRequest struct {
	ProjectID  uuid.UUID `json:"project_id,omitempty"`
	ReasonCode string    `json:"reason_code,omitempty"`
}

func (h *DeploymentHandler) List(w http.ResponseWriter, r *http.Request) {
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
	clusterID, err := optionalUUIDFilter(r, "cluster_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_filter", err.Error())
		return
	}
	phase, err := deploymentPhaseFilter(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_filter", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery deployment persistence is unavailable")
		return
	}
	params := sqlc.ListClusterDeploymentsParams{ProjectID: projectID, ClusterID: clusterID, Phase: phase, QueryLimit: limit, QueryOffset: offset}
	rows, err := h.queries.ListClusterDeployments(r.Context(), params)
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	total, err := h.queries.CountClusterDeployments(r.Context(), sqlc.CountClusterDeploymentsParams{ProjectID: projectID, ClusterID: clusterID, Phase: phase})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	respondPage(w, r, rows, total, limit, offset, int64(offset)+int64(len(rows)) < total, true)
}

func (h *DeploymentHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID, deploymentID, ok := deploymentScope(w, r)
	if !ok {
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery deployment persistence is unavailable")
		return
	}
	row, err := h.queries.GetClusterDeployment(r.Context(), sqlc.GetClusterDeploymentParams{ID: deploymentID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	eventRows, err := h.queries.ListClusterDeploymentEvents(r.Context(), sqlc.ListClusterDeploymentEventsParams{DeploymentID: deploymentID, ProjectID: projectID, QueryLimit: 50, QueryOffset: 0})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	setEntityTag(w, row.DesiredGeneration)
	respondData(w, http.StatusOK, map[string]any{"deployment": row, "events": eventRows})
}

func (h *DeploymentHandler) Events(w http.ResponseWriter, r *http.Request) {
	projectID, deploymentID, ok := deploymentScope(w, r)
	if !ok {
		return
	}
	limit, offset, err := parsePagination(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	rows, err := h.queries.ListClusterDeploymentEvents(r.Context(), sqlc.ListClusterDeploymentEventsParams{DeploymentID: deploymentID, ProjectID: projectID, QueryLimit: limit, QueryOffset: offset})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	total, err := h.queries.CountClusterDeploymentEvents(r.Context(), sqlc.CountClusterDeploymentEventsParams{DeploymentID: deploymentID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	respondPage(w, r, rows, total, limit, offset, int64(offset)+int64(len(rows)) < total, true)
}

func (h *DeploymentHandler) Reconcile(w http.ResponseWriter, r *http.Request) {
	h.action(w, r, deliverydeployment.ActionReconcile)
}

func (h *DeploymentHandler) Suspend(w http.ResponseWriter, r *http.Request) {
	h.action(w, r, deliverydeployment.ActionSuspend)
}

func (h *DeploymentHandler) Resume(w http.ResponseWriter, r *http.Request) {
	h.action(w, r, deliverydeployment.ActionResume)
}

func (h *DeploymentHandler) action(w http.ResponseWriter, r *http.Request, action deliverydeployment.Action) {
	expected, err := requireIfMatch(r)
	if err != nil {
		respondError(w, http.StatusPreconditionRequired, "if_match_required", err.Error())
		return
	}
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request deploymentActionRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, request.ProjectID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	deploymentID, err := pathUUID(r, "id", "deploymentID")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.controller == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery deployment controls are unavailable")
		return
	}
	result, err := h.controller.Act(r.Context(), deliverydeployment.Request{
		ProjectID: projectID, DeploymentID: deploymentID, ExpectedGeneration: expected,
		Action: action, ReasonCode: request.ReasonCode,
	})
	if err != nil {
		if errors.Is(err, deliverydeployment.ErrStaleGeneration) {
			respondError(w, http.StatusPreconditionFailed, "stale_generation", err.Error())
			return
		}
		respondDatabaseError(w, err)
		return
	}
	setEntityTag(w, result.Deployment.DesiredGeneration)
	events.PublishChanged(h.bus, "cluster_deployment", result.Deployment.ClusterID.String(), deploymentID.String(), map[string]any{"project_id": projectID.String(), "action": string(action)})
	recordAudit(r, h.queries, deploymentAuditAction(action), "cluster_deployment", deploymentID.String(), "", map[string]any{
		"project_id":         projectID.String(),
		"cluster_id":         result.Deployment.ClusterID.String(),
		"target_id":          result.Deployment.TargetID.String(),
		"phase":              result.Deployment.Phase,
		"desired_generation": result.Deployment.DesiredGeneration,
	})
	respondData(w, http.StatusAccepted, map[string]any{"deployment": result.Deployment, "event": result.Event})
}

func deploymentAuditAction(action deliverydeployment.Action) string {
	switch action {
	case deliverydeployment.ActionReconcile:
		return "delivery.deployment.reconcile_requested"
	case deliverydeployment.ActionSuspend:
		return "delivery.deployment.suspension_requested"
	case deliverydeployment.ActionResume:
		return "delivery.deployment.resume_requested"
	default:
		return "delivery.deployment.action_requested"
	}
}

func deploymentScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	projectID, err := projectIDFromRequest(r, uuid.Nil)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return uuid.Nil, uuid.Nil, false
	}
	deploymentID, err := pathUUID(r, "id", "deploymentID")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, deploymentID, true
}

func optionalUUIDFilter(r *http.Request, name string) (pgtype.UUID, error) {
	values, ok := r.URL.Query()[name]
	if !ok {
		return pgtype.UUID{}, nil
	}
	if len(values) != 1 {
		return pgtype.UUID{}, errors.New(name + " must occur exactly once")
	}
	id, err := uuid.Parse(values[0])
	if err != nil || id == uuid.Nil {
		return pgtype.UUID{}, errors.New(name + " must be a non-zero UUID")
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

func deploymentPhaseFilter(r *http.Request) (pgtype.Text, error) {
	values, ok := r.URL.Query()["phase"]
	if !ok {
		return pgtype.Text{}, nil
	}
	if len(values) != 1 {
		return pgtype.Text{}, errors.New("phase must occur exactly once")
	}
	allowed := map[string]bool{
		"pending": true, "blocked": true, "applying": true, "ready": true,
		"degraded": true, "failed": true, "suspended": true, "deleting": true,
		"removed": true, "unknown": true,
	}
	phase := strings.TrimSpace(values[0])
	if !allowed[phase] {
		return pgtype.Text{}, errors.New("phase is not a supported deployment phase")
	}
	return pgtype.Text{String: phase, Valid: true}, nil
}
