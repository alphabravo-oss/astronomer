// Package tasks — F6 shared fleet-sweep scaffolding.
//
// The periodic worker sweeps (alert:evaluate, mesh:detect,
// gatekeeper:policy_apply, cluster:health_check) each walk the WHOLE fleet
// once per tick under a single leader lease. Historically they did so
// SERIALLY with a per-cluster tunnel/DB/monitoring round-trip and no
// aggregate deadline, so at ~100–200 clusters the wall-clock exceeded the
// tick interval and a handful of slow/disconnected agents stalled the entire
// sweep (the scheduler kept enqueuing on top of the stuck run).
//
// This file centralises the three knobs the F6 fix hangs on:
//   - fleetSweepConcurrency: bounded fan-out width (mirrors the
//     resources_search.go handler's SetLimit(16)).
//   - fanOutClusters: a bounded-concurrency, per-cluster-timeout iterator that
//     skips-with-log rather than failing the sweep on one slow cluster and
//     stops scheduling new work once the aggregate sweep deadline elapses.
//   - the per-sweep aggregate deadlines / per-cluster timeouts each handler
//     applies (defined next to their handlers).

package tasks

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fleetSweepConcurrency caps how many clusters a periodic fleet sweep touches
// in parallel. Matches resourcesSearchMaxConcurrency (handler
// resources_search.go): wide enough to keep the aggregate wall-clock ~
// (fleet/16)×per-cluster instead of fleet×per-cluster, narrow enough not to
// exhaust the pgx pool or open hundreds of simultaneous tunnel round-trips.
const fleetSweepConcurrency = 16

// fanOutClusters runs fn for every cluster with bounded concurrency and a
// per-cluster timeout. It never returns an error and never aborts the batch on
// a single cluster: fn is responsible for logging its own failures, so one
// slow/disconnected agent is skipped-with-log rather than stalling the tick or
// failing the whole sweep.
//
// It honours the parent ctx deadline (the per-sweep aggregate deadline the
// caller installs): once that fires, gctx is cancelled, in-flight per-cluster
// contexts observe the cancellation, and no further clusters are scheduled. A
// sweep can therefore never run materially past its tick interval even if the
// tail of the fleet is unreachable.
func fanOutClusters(ctx context.Context, clusters []sqlc.Cluster, perCluster time.Duration, fn func(ctx context.Context, c sqlc.Cluster)) {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fleetSweepConcurrency)
	for _, c := range clusters {
		c := c
		// Stop scheduling new work once the aggregate deadline (or a parent
		// cancellation) has fired — the remaining clusters are skipped and
		// converge on the next tick.
		if gctx.Err() != nil {
			break
		}
		g.Go(func() error {
			cctx, cancel := context.WithTimeout(gctx, perCluster)
			defer cancel()
			fn(cctx, c)
			return nil // never bubble — partial failure is expected/skipped
		})
	}
	_ = g.Wait() // goroutines never return an error, so this never errors
}
