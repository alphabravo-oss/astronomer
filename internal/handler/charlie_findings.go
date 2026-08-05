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
	Transition(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string) (charlie.FindingView, error)
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
	items := make([]map[string]any, 0, len(views))
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
	h.transition(w, r, "acknowledged")
}

func (h *CharlieFindingHandler) Dismiss(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, "dismissed")
}

func (h *CharlieFindingHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, "resolved")
}

func (h *CharlieFindingHandler) transition(w http.ResponseWriter, r *http.Request, next string) {
	actor, findingID, ok := charlieFindingActorAndID(w, r)
	if !ok {
		return
	}
	var request charlieFindingTransitionRequest
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	requestID, err := uuid.Parse(request.RequestID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie finding transition")
		return
	}
	view, err := h.access.Transition(r.Context(), mustUserID(actor), findingID, requestID, next)
	if err != nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Charlie finding transition could not be completed")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"finding": safeCharlieFinding(view, true)})
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

func safeCharlieFinding(view charlie.FindingView, includeDetail bool) map[string]any {
	row := view.Finding
	severity := charlie.NormalizeFindingSeverity(row.Severity)
	item := map[string]any{
		"id": row.ID, "title": row.Title, "severity": severity, "state": row.Status,
		"summary": row.Summary, "reason_no_action": row.ExecutionBlockCode,
		"repeat_count": row.RepeatCount, "updated_at": row.UpdatedAt,
	}
	if len(view.Resources) > 0 {
		resource := view.Resources[0]
		item["affected_resource"] = map[string]any{"type": resource.ResourceType, "id": resource.ResourceID, "required_verb": resource.RequiredVerb}
	}
	if includeDetail {
		item["risk_impact"] = row.RiskImpact
		item["verification_summary"] = row.VerificationSummary
		if row.RecommendedActionLabel != "" {
			proposed := map[string]any{"label": row.RecommendedActionLabel, "mode": row.EffectiveMode, "eligible": false}
			if row.ApprovalID.Valid {
				proposed["approval_id"] = row.ApprovalID.String
				proposed["eligible"] = (row.Status == "open" || row.Status == "acknowledged") && row.ExecutionBlockCode == "approval_required" && row.ExpiresAt.Valid && row.ExpiresAt.Time.After(time.Now())
			}
			item["proposed_action"] = proposed
		}
		if len(view.Remote) > 0 {
			item["detail"] = view.Remote
		}
	}
	return item
}
