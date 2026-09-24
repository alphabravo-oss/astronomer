package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

// platformCatalogProject selects Delivery ownership for an installation whose
// namespace has no user-project owner. Call only after authorizing the original
// cluster/namespace target WITHOUT a project grant. This does not assign the
// namespace to a project or extend any caller's permissions.
func (h *CatalogHandler) platformCatalogProject(ctx context.Context, clusterID uuid.UUID) (uuid.UUID, error) {
	store, ok := h.queries.(interface {
		GetProjectByNameAndCluster(context.Context, sqlc.GetProjectByNameAndClusterParams) (sqlc.Project, error)
	})
	if !ok {
		return uuid.Nil, errors.New("platform delivery project lookup is unavailable")
	}
	project, err := store.GetProjectByNameAndCluster(ctx, sqlc.GetProjectByNameAndClusterParams{
		Name: "astronomer-system", ClusterID: clusterID,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("platform delivery project is unavailable; assign the namespace to a project before installing: %w", err)
	}
	if project.ClusterID != clusterID || project.ManagedBy != "system" || project.ID == uuid.Nil {
		return uuid.Nil, errors.New("platform delivery project identity is invalid")
	}
	return project.ID, nil
}
