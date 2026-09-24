package handler

import (
	"context"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Agent inventory is platform-authorized at the route before these canonical
// cluster filters run. Do not use the unscoped variants for scoped routes.
type clusterAgentSearchQuerier interface {
	CountClustersFiltered(context.Context, sqlc.CountClustersFilteredParams) (int64, error)
	ListClustersFiltered(context.Context, sqlc.ListClustersFilteredParams) ([]sqlc.Cluster, error)
	ListClustersFilteredAfter(context.Context, sqlc.ListClustersFilteredAfterParams) ([]sqlc.Cluster, error)
}
