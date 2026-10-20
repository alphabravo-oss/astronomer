package handler

import (
	"context"

	"github.com/google/uuid"
)

// inFlightDecommissionSet returns which of the given cluster IDs have a
// pending/running decommission. Best-effort — returns an empty set on any
// error. The query receives only the already-authorized page IDs, so work is
// bounded by the page size and no estate-wide decommission state is read.
func (h *ClusterHandler) inFlightDecommissionSet(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]bool {
	set := map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return set
	}
	rows, err := h.queries.ListPendingClusterDecommissionsForClusters(ctx, ids)
	if err != nil {
		return set
	}
	for _, row := range rows {
		set[row.ClusterID] = true
	}
	return set
}
