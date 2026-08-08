package handler

import (
	"context"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type CharlieOperationAccess interface {
	Get(context.Context, uuid.UUID, string) (sqlc.CharlieActionReceipt, error)
}

type CharlieOperationHandler struct{ access CharlieOperationAccess }

func NewCharlieOperationHandler(access CharlieOperationAccess) *CharlieOperationHandler {
	return &CharlieOperationHandler{access: access}
}

func (h *CharlieOperationHandler) Get(w http.ResponseWriter, r *http.Request) {
	actor, ok := browserCharlieActor(w, r)
	if !ok {
		return
	}
	operationID := chi.URLParam(r, "operation_id")
	if h == nil || h.access == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie operation status is unavailable")
		return
	}
	receipt, err := h.access.Get(r.Context(), mustUserID(actor), operationID)
	if err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Charlie operation access is denied")
		return
	}
	response := map[string]any{
		"operation_id":  receipt.CharlieActionID,
		"capability":    receipt.Capability,
		"effect":        receipt.Effect,
		"state":         receipt.State,
		"result_status": receipt.ResultStatus,
		"created_at":    receipt.CreatedAt,
		"updated_at":    receipt.UpdatedAt,
	}
	if receipt.DispatchedAt.Valid {
		response["dispatched_at"] = receipt.DispatchedAt.Time
	}
	if receipt.VerifiedAt.Valid {
		response["verified_at"] = receipt.VerifiedAt.Time
	}
	RespondJSON(w, http.StatusOK, response)
}
