package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie approvals are unavailable")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie approval decision")
		return
	}
	decision := request.Decision
	if decision == "deny" {
		decision = "reject"
	}
	if decision != "approve" && decision != "reject" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie approval decision")
		return
	}
	view, err := h.access.Decide(r.Context(), mustUserID(actor), approvalID, requestID, decision, request.Rationale)
	if err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Charlie approval was not accepted; no action was authorized")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"approval": view})
}
