package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type clusterOwnershipQuerier interface {
	GetClusterOwnership(ctx context.Context, id uuid.UUID) (sqlc.FleetOwnership, error)
}

type clusterOwnershipTransferQuerier interface {
	clusterOwnershipQuerier
	SetClusterOwnership(ctx context.Context, arg sqlc.SetClusterOwnershipParams) (sqlc.FleetOwnership, error)
}

// sqlc generates distinct row types for Get/Set/List even though the columns
// are identical. Keep the legacy FleetOwnership-shaped seam for narrow fakes,
// and adapt the generated production surface explicitly.
type clusterOwnershipSQLQuerier interface {
	GetClusterOwnership(context.Context, uuid.UUID) (sqlc.GetClusterOwnershipRow, error)
	SetClusterOwnership(context.Context, sqlc.SetClusterOwnershipParams) (sqlc.SetClusterOwnershipRow, error)
}

func clusterUpdateBlockedByOwnership(ctx context.Context, q any, id uuid.UUID) (string, error) {
	ownership, ok, err := readClusterOwnership(ctx, q, id)
	if !ok {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if ownership.ManagedBy != "crd" {
		return "", nil
	}
	return fmt.Sprintf("Cluster is managed by CRD %s/%s %s/%s; edit the Kubernetes resource or transfer ownership before using this API.",
		ownership.ExternalRefApiVersion,
		ownership.ExternalRefKind,
		ownership.ExternalRefNamespace,
		ownership.ExternalRefName,
	), nil
}

func fleetOwnershipFromClusterGet(row sqlc.GetClusterOwnershipRow) sqlc.FleetOwnership {
	return sqlc.FleetOwnership(row)
}

func fleetOwnershipFromClusterSet(row sqlc.SetClusterOwnershipRow) sqlc.FleetOwnership {
	return sqlc.FleetOwnership(row)
}

func readClusterOwnership(ctx context.Context, q any, id uuid.UUID) (sqlc.FleetOwnership, bool, error) {
	if legacy, ok := q.(clusterOwnershipQuerier); ok {
		row, err := legacy.GetClusterOwnership(ctx, id)
		return row, true, err
	}
	if generated, ok := q.(clusterOwnershipSQLQuerier); ok {
		row, err := generated.GetClusterOwnership(ctx, id)
		return fleetOwnershipFromClusterGet(row), true, err
	}
	return sqlc.FleetOwnership{}, false, nil
}

func writeClusterOwnership(ctx context.Context, q any, arg sqlc.SetClusterOwnershipParams) (sqlc.FleetOwnership, bool, error) {
	if legacy, ok := q.(clusterOwnershipTransferQuerier); ok {
		row, err := legacy.SetClusterOwnership(ctx, arg)
		return row, true, err
	}
	if generated, ok := q.(clusterOwnershipSQLQuerier); ok {
		row, err := generated.SetClusterOwnership(ctx, arg)
		return fleetOwnershipFromClusterSet(row), true, err
	}
	return sqlc.FleetOwnership{}, false, nil
}

// TakeoverOwnership handles POST /api/v1/clusters/{id}/ownership/takeover/.
//
// Ordinary PUT/PATCH still rejects CRD-owned rows. This explicit endpoint is
// the operator escape hatch: it clears the CR external_ref metadata and moves
// the row back to API ownership so future UI/API edits are intentional.
func (h *ClusterHandler) TakeoverOwnership(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	type takeoverResult struct {
		previous    sqlc.FleetOwnership
		updated     sqlc.FleetOwnership
		transferred bool
	}
	takeover, err := executeMutation(r, h.runTx,
		func(q ClusterMutationTx) (takeoverResult, error) {
			previous, updated, transferred, mutationErr := transferClusterOwnershipToAPI(r.Context(), q, id)
			return takeoverResult{previous: previous, updated: updated, transferred: transferred}, mutationErr
		},
		func(result takeoverResult) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.ownership.takeover", resourceType: "cluster", resourceID: id.String(), resourceName: cluster.Name,
				status: http.StatusOK,
				detail: map[string]any{
					"previous_managed_by": result.previous.ManagedBy,
					"previous_ref": map[string]string{
						"api_version": result.previous.ExternalRefApiVersion,
						"kind":        result.previous.ExternalRefKind, "namespace": result.previous.ExternalRefNamespace,
						"name": result.previous.ExternalRefName,
					},
					"transferred": result.transferred,
				},
			}
		})
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		case errors.Is(err, errClusterOwnershipTransferUnsupported):
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Only CRD-owned clusters can be transferred through this endpoint")
		default:
			respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to transfer cluster ownership")
		}
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"id":          takeover.updated.ID.String(),
		"managed_by":  takeover.updated.ManagedBy,
		"transferred": takeover.transferred,
	})
}

func transferClusterOwnershipToAPI(ctx context.Context, q any, id uuid.UUID) (sqlc.FleetOwnership, sqlc.FleetOwnership, bool, error) {
	previous, ok, err := readClusterOwnership(ctx, q, id)
	if !ok {
		return sqlc.FleetOwnership{}, sqlc.FleetOwnership{}, false, fmt.Errorf("cluster ownership transfer query support is not configured")
	}
	if err != nil {
		return sqlc.FleetOwnership{}, sqlc.FleetOwnership{}, false, err
	}
	switch previous.ManagedBy {
	case "crd":
		updated, supported, err := writeClusterOwnership(ctx, q, sqlc.SetClusterOwnershipParams{
			ID:        id,
			ManagedBy: "api",
		})
		if !supported {
			return previous, sqlc.FleetOwnership{}, false, fmt.Errorf("cluster ownership transfer query support is not configured")
		}
		return previous, updated, true, err
	case "api", "ui":
		return previous, previous, false, nil
	default:
		return previous, sqlc.FleetOwnership{}, false, errClusterOwnershipTransferUnsupported
	}
}
