package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type CharlieFindingAccess interface {
	List(context.Context, uuid.UUID, string, int32, int32) ([]charlie.FindingView, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (charlie.FindingView, error)
	TransitionAdvisory(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, charlie.FindingAdvisoryDecision) (charlie.FindingView, error)
	TransitionWorkflow(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string) (charlie.FindingView, error)
}

type CharlieFindingHandler struct{ access CharlieFindingAccess }

func NewCharlieFindingHandler(access CharlieFindingAccess) *CharlieFindingHandler {
	return &CharlieFindingHandler{access: access}
}

// openapi:request CharlieFindingTransitionRequest
type charlieFindingTransitionRequest struct {
	RequestID string `json:"request_id"`
}

func (h *CharlieFindingHandler) List(w http.ResponseWriter, r *http.Request) {
	actor, ok := browserCharlieActor(w, r)
	if !ok {
		return
	}
	offset, ok := boundedQueryInt(w, r, "offset", 0, 0, 100000)
	if !ok {
		return
	}
	limit, ok := boundedQueryInt(w, r, "limit", 20, 1, charlie.MaxCharlieFindingItems)
	if !ok {
		return
	}
	views, err := h.access.List(r.Context(), mustUserID(actor), strings.TrimSpace(r.URL.Query().Get("status")), int32(offset), int32(limit))
	if err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Charlie finding access is denied")
		return
	}
	items := make([]charlieFindingResponse, 0, len(views))
	for _, view := range views {
		items = append(items, safeCharlieFinding(view, false))
	}
	RespondJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *CharlieFindingHandler) Get(w http.ResponseWriter, r *http.Request) {
	actor, findingID, ok := charlieFindingActorAndID(w, r)
	if !ok {
		return
	}
	view, err := h.access.Get(r.Context(), mustUserID(actor), findingID)
	if err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Charlie finding access is denied")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"finding": safeCharlieFinding(view, true)})
}

func (h *CharlieFindingHandler) Acknowledge(w http.ResponseWriter, r *http.Request) {
	h.transitionAdvisory(w, r, charlie.FindingAdvisoryAcknowledge)
}

func (h *CharlieFindingHandler) StartRemediation(w http.ResponseWriter, r *http.Request) {
	h.transitionWorkflow(w, r, "start_remediation")
}

func (h *CharlieFindingHandler) RequestVerification(w http.ResponseWriter, r *http.Request) {
	h.transitionWorkflow(w, r, "request_verification")
}

func (h *CharlieFindingHandler) Dismiss(w http.ResponseWriter, r *http.Request) {
	h.transitionAdvisory(w, r, charlie.FindingAdvisoryDismiss)
}

func (h *CharlieFindingHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	h.transitionAdvisory(w, r, charlie.FindingAdvisoryResolve)
}

func (h *CharlieFindingHandler) transitionAdvisory(w http.ResponseWriter, r *http.Request, next charlie.FindingAdvisoryDecision) {
	actor, findingID, requestID, ok := charlieFindingTransition(w, r)
	if !ok {
		return
	}
	view, err := h.access.TransitionAdvisory(r.Context(), mustUserID(actor), findingID, requestID, next)
	if err != nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Charlie finding transition could not be completed")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"finding": safeCharlieFinding(view, true)})
}

func (h *CharlieFindingHandler) transitionWorkflow(w http.ResponseWriter, r *http.Request, next string) {
	actor, findingID, requestID, ok := charlieFindingTransition(w, r)
	if !ok {
		return
	}
	view, err := h.access.TransitionWorkflow(r.Context(), mustUserID(actor), findingID, requestID, next)
	if err != nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Charlie finding transition could not be completed")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"finding": safeCharlieFinding(view, true)})
}

func charlieFindingTransition(w http.ResponseWriter, r *http.Request) (*appmiddleware.AuthenticatedUser, uuid.UUID, uuid.UUID, bool) {
	actor, findingID, ok := charlieFindingActorAndID(w, r)
	if !ok {
		return nil, uuid.Nil, uuid.Nil, false
	}
	var request charlieFindingTransitionRequest
	if !decodeCharlieJSON(w, r, &request) {
		return nil, uuid.Nil, uuid.Nil, false
	}
	requestID, err := uuid.Parse(request.RequestID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie finding transition")
		return nil, uuid.Nil, uuid.Nil, false
	}
	return actor, findingID, requestID, true
}

func charlieFindingActorAndID(w http.ResponseWriter, r *http.Request) (*appmiddleware.AuthenticatedUser, uuid.UUID, bool) {
	actor, ok := browserCharlieActor(w, r)
	if !ok {
		return nil, uuid.Nil, false
	}
	findingID, err := uuid.Parse(chi.URLParam(r, "finding_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie finding ID")
		return nil, uuid.Nil, false
	}
	return actor, findingID, true
}

type charlieFindingResourceResponse struct {
	Type         string `json:"type"`
	ID           string `json:"id"`
	RequiredVerb string `json:"required_verb"`
}

type charlieFindingResponse struct {
	ID                 uuid.UUID                       `json:"id"`
	Title              string                          `json:"title"`
	Severity           string                          `json:"severity"`
	State              string                          `json:"state"`
	Summary            string                          `json:"summary"`
	ReasonNoAction     string                          `json:"reason_no_action"`
	RepeatCount        int32                           `json:"repeat_count"`
	Source             string                          `json:"source"`
	CreatedAt          time.Time                       `json:"created_at"`
	UpdatedAt          time.Time                       `json:"updated_at"`
	WorkflowState      charlie.FindingWorkflowState    `json:"workflow_state"`
	AvailableDecisions []string                        `json:"available_decisions"`
	SessionID          *uuid.UUID                      `json:"session_id,omitempty"`
	AffectedResource   *charlieFindingResourceResponse `json:"affected_resource,omitempty"`
	RiskImpact         string                          `json:"risk_impact,omitempty"`
	Verification       string                          `json:"verification_summary,omitempty"`
	Detail             *charlie.FindingAdvisoryDetail  `json:"detail,omitempty"`
}

func safeCharlieFinding(view charlie.FindingView, includeDetail bool) charlieFindingResponse {
	row := view.Finding
	severity := charlie.NormalizeFindingSeverity(row.Severity)
	workflow := charlie.FindingWorkflowFor(row, time.Now().UTC())
	item := charlieFindingResponse{
		ID: row.ID, Title: row.Title, Severity: severity, State: row.Status,
		Summary: row.Summary, ReasonNoAction: row.ExecutionBlockCode,
		RepeatCount: row.RepeatCount, Source: row.Source,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		WorkflowState: workflow.State, AvailableDecisions: workflow.Decisions,
	}
	if row.SessionID.Valid {
		sessionID := uuid.UUID(row.SessionID.Bytes)
		item.SessionID = &sessionID
	}
	if len(view.Resources) > 0 {
		resource := view.Resources[0]
		item.AffectedResource = &charlieFindingResourceResponse{Type: resource.ResourceType, ID: resource.ResourceID, RequiredVerb: resource.RequiredVerb}
	}
	if includeDetail {
		item.RiskImpact = row.RiskImpact
		item.Verification = row.VerificationSummary
		item.Detail = view.Detail
	}
	return item
}
