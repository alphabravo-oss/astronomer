package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/charlie"
)

type CharlieApprovalAccess interface {
	List(context.Context, uuid.UUID) ([]charlie.ApprovalView, error)
	Decide(context.Context, uuid.UUID, string, uuid.UUID, string, string) (charlie.ApprovalView, error)
}

type CharlieApprovalHandler struct{ access CharlieApprovalAccess }

func NewCharlieApprovalHandler(access CharlieApprovalAccess) *CharlieApprovalHandler {
	return &CharlieApprovalHandler{access: access}
}

func (h *CharlieApprovalHandler) List(w http.ResponseWriter, r *http.Request) {
	actor, ok := browserCharlieActor(w, r)
	if !ok {
		return
	}
	items, err := h.access.List(r.Context(), mustUserID(actor))
	if err != nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.InternalError, "Charlie approvals are unavailable")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"items": items})
}

// openapi:request CharlieApprovalDecisionRequest
type decideCharlieApprovalRequest struct {
	RequestID string `json:"request_id"`
	Decision  string `json:"decision"`
	Rationale string `json:"rationale,omitempty"`
}

func (h *CharlieApprovalHandler) Decide(w http.ResponseWriter, r *http.Request) {
	actor, ok := browserCharlieActor(w, r)
	if !ok {
		return
	}
	approvalID := strings.TrimSpace(chi.URLParam(r, "approval_id"))
	var request decideCharlieApprovalRequest
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	requestID, err := uuid.Parse(request.RequestID)
	if err != nil || approvalID == "" || len(approvalID) > 128 || len(request.Rationale) > 512 {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Invalid Charlie approval decision")
		return
	}
	decision := request.Decision
	if decision == "deny" {
		decision = "reject"
	}
	if decision != "approve" && decision != "reject" {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Invalid Charlie approval decision")
		return
	}
	view, err := h.access.Decide(r.Context(), mustUserID(actor), approvalID, requestID, decision, request.Rationale)
	if err != nil {
		if charlieApprovalConflict(err) {
			apiutil.WriteError(w, r, http.StatusConflict, apiutil.Conflict, "This exact Charlie approval is stale or was already decided; no action was authorized")
			return
		}
		apiutil.WriteError(w, r, http.StatusForbidden, apiutil.Forbidden, "Charlie approval was not accepted; no action was authorized")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"approval": view})
}

func charlieApprovalConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "stale") || strings.Contains(message, "not pending") ||
		strings.Contains(message, "already") || strings.Contains(message, "expired") ||
		strings.Contains(message, "conflict")
}
