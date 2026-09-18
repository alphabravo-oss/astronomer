package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h *ProjectHandler) TakeoverOwnership(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
		return
	}
	project, err := h.queries.GetProjectByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}
	previous, updated, transferred, err := transferProjectOwnershipToAPI(r.Context(), h.queries, id)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		case errors.Is(err, errProjectOwnershipTransferUnsupported):
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Only CRD-owned projects can be transferred through this endpoint")
		default:
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to transfer project ownership")
		}
		return
	}
	h.recordProjectAudit(r, "project.ownership.takeover", project, map[string]any{
		"previous_managed_by": previous.ManagedBy,
		"previous_ref": map[string]string{
			"api_version": previous.ExternalRefApiVersion,
			"kind":        previous.ExternalRefKind,
			"namespace":   previous.ExternalRefNamespace,
			"name":        previous.ExternalRefName,
		},
		"transferred": transferred,
	})
	RespondJSON(w, http.StatusOK, map[string]any{
		"id":          updated.ID.String(),
		"managed_by":  updated.ManagedBy,
		"transferred": transferred,
	})
}

func transferProjectOwnershipToAPI(ctx context.Context, q any, id uuid.UUID) (sqlc.FleetOwnership, sqlc.FleetOwnership, bool, error) {
	ownershipQ, ok := q.(projectOwnershipTransferQuerier)
	if !ok {
		return sqlc.FleetOwnership{}, sqlc.FleetOwnership{}, false, fmt.Errorf("project ownership transfer query support is not configured")
	}
	previous, err := ownershipQ.GetProjectOwnership(ctx, id)
	if err != nil {
		return sqlc.FleetOwnership{}, sqlc.FleetOwnership{}, false, err
	}
	switch previous.ManagedBy {
	case "crd":
		updated, err := ownershipQ.SetProjectOwnership(ctx, sqlc.SetProjectOwnershipParams{
			ID:        id,
			ManagedBy: "api",
		})
		return previous, updated, true, err
	case "api", "ui":
		return previous, previous, false, nil
	default:
		return previous, sqlc.FleetOwnership{}, false, errProjectOwnershipTransferUnsupported
	}
}

// UpdatePolicy handles PATCH /api/v1/projects/{id}/policy/.
//
// Updates only the per-project policy fields (pod_security_profile, the three
// resource_quota_* limits). The next reconciler tick picks up the new policy
// — every current namespace is re-enqueued immediately because changing the
// total cap rebalances every namespace allocation.
//
// All four fields are optional. Missing fields keep their current value, so
// a caller can change just the PSS profile without resending quota numbers.
