package tasks

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// clusterSweepConcurrency caps the parallel work performed by periodic jobs
// that inspect every active cluster. It keeps sweep duration bounded without
// exhausting the database pool or tunnel connections.
const clusterSweepConcurrency = 16

// fanOutClusters invokes fn for every cluster with bounded concurrency and a
// per-cluster timeout. A failed or unavailable cluster must not block the
// remaining clusters; fn owns its own error logging.
func fanOutClusters(ctx context.Context, clusters []sqlc.Cluster, perCluster time.Duration, fn func(context.Context, sqlc.Cluster)) {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(clusterSweepConcurrency)
	for _, cluster := range clusters {
		if gctx.Err() != nil {
			break
		}
		cluster := cluster
		g.Go(func() error {
			clusterCtx, cancel := context.WithTimeout(gctx, perCluster)
			defer cancel()
			fn(clusterCtx, cluster)
			return nil
		})
	}
	_ = g.Wait()
}

const clusterSweepPageSize int32 = 500

// listAllClustersPaged loads every non-decommissioned cluster without a
// fixed-row cap, which prevents large installations from silently omitting
// clusters from periodic work.
func listAllClustersPaged(ctx context.Context, list func(context.Context, sqlc.ListClustersParams) ([]sqlc.Cluster, error)) ([]sqlc.Cluster, error) {
	var all []sqlc.Cluster
	for offset := int32(0); ; offset += clusterSweepPageSize {
		page, err := list(ctx, sqlc.ListClustersParams{Limit: clusterSweepPageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if int32(len(page)) < clusterSweepPageSize {
			return all, nil
		}
	}
}
