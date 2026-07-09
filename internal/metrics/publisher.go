// Package metrics runs background tickers that fan out cluster metrics and
// status-transition events onto the SSE bus. The goal is to keep the UI
// "alive" without requiring per-page polling: subscribers see CPU/mem ticks
// and active<->disconnected flips the moment the server side notices them.
//
// Two tickers run in one goroutine pair:
//
//   - metrics tick (default 10s): for every cluster currently marked active
//     in the database, fetch a snapshot via the existing clustermetrics
//     provider (fast path uses the in-process k8s client for the local
//     cluster; remote clusters round-trip through the agent tunnel) and
//     publish a cluster.metrics event.
//
//   - status sweep (default 15s): scan every cluster, compare last_heartbeat
//     against a 60s grace window, and flip status active<->disconnected as
//     needed. Each transition is persisted via UpdateClusterStatus and
//     fanned out as cluster.status_changed.
//
// Errors are intentionally swallowed at this layer; the publisher must never
// crash the server. Bus.Publish completes after local broadcast plus a bounded,
// nonblocking Redis-relay enqueue, so Redis latency cannot stall a fleet-wide
// metrics pass. Relay queue overload is intentionally lossy and observable.
package metrics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/clustermetrics"
)

const (
	// defaultMetricsInterval is how often we emit cluster.metrics for each
	// active cluster. 10s matches the dashboard's "feels live" target while
	// staying well under the clustermetrics provider's 30s cache TTL — most
	// ticks end up free (cache hit) with the first miss inside each TTL
	// window paying the round-trip cost.
	defaultMetricsInterval = 10 * time.Second

	// defaultStatusSweepInterval is how often we check for cluster
	// active<->disconnected transitions. The agent's heartbeat cadence is
	// 30s so this catches a missed beat within ~1.5 cycles.
	defaultStatusSweepInterval = 15 * time.Second

	// staleHeartbeatThreshold is the age past which an "active" cluster gets
	// flipped to "disconnected". This MUST match the worker health-check window
	// (internal/worker/tasks/health_check.go, 2m) — the two are independent
	// staleness writers of clusters.status, and a DIFFERENT threshold makes them
	// fight in the gap between the two values, flapping a cluster
	// active<->disconnected (M3). 2m is also C1's disconnect window: a degraded
	// agent still beats every ~30s, so a tunnel-healthy cluster never crosses it.
	// This sweep is the AUTHORITATIVE writer (transition-only, event-emitting,
	// status-preserving, local-exempt); the worker check is a coarser backstop.
	staleHeartbeatThreshold = 2 * time.Minute

	// metricsFanoutConcurrency bounds how many per-cluster snapshot fetches run
	// in parallel each tick. A serial loop paid up to the 4s stall timeout ONE
	// cluster at a time, so a few hundred active clusters — or a partition that
	// stalls many agents at once — made a single pass take minutes, far longer
	// than the 10s cadence, so the cache never stayed warm and most clusters
	// published stale/zero metrics forever. A bounded fan-out keeps one pass to
	// roughly ceil(active/concurrency) timeout windows regardless of fleet size
	// while still capping concurrent tunnel round-trips.
	metricsFanoutConcurrency = 16
)

// ClusterQuerier is the minimal subset of sqlc.Queries the publisher needs.
// Declared as an interface so tests can swap a fake without standing up a
// real database. *sqlc.Queries satisfies this naturally.
type ClusterQuerier interface {
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	// UpdateClusterStatusOnHeartbeat is the authoritative status writer for
	// this publisher (CORR-02): it re-checks the 2m liveness window at write
	// time so a mid-sweep reconnect is not clobbered back to disconnected.
	UpdateClusterStatusOnHeartbeat(ctx context.Context, arg sqlc.UpdateClusterStatusOnHeartbeatParams) (int64, error)
}

// MetricsProvider is the minimal interface the publisher uses to fetch a
// fresh metrics snapshot for a cluster. *clustermetrics.Provider implements
// this (and Get already serves cached snapshots in <1ms when fresh).
type MetricsProvider interface {
	Get(ctx context.Context, clusterID string, isLocal bool) clustermetrics.Snapshot
}

// Publisher drives the periodic fan-out. Construct via New, then Start.
// Start spawns goroutines and returns immediately; cancel ctx to stop them.
type Publisher struct {
	bus        *events.Bus
	queries    ClusterQuerier
	metrics    MetricsProvider
	log        *slog.Logger
	tickEvery  time.Duration
	sweepEvery time.Duration
	threshold  time.Duration
}

// Option configures a Publisher.
type Option func(*Publisher)

// WithMetricsInterval overrides the per-cluster metrics-publish cadence.
func WithMetricsInterval(d time.Duration) Option {
	return func(p *Publisher) {
		if d > 0 {
			p.tickEvery = d
		}
	}
}

// WithStatusSweepInterval overrides the active<->disconnected sweep cadence.
func WithStatusSweepInterval(d time.Duration) Option {
	return func(p *Publisher) {
		if d > 0 {
			p.sweepEvery = d
		}
	}
}

// WithStaleThreshold overrides how long since last_heartbeat counts as
// disconnected.
func WithStaleThreshold(d time.Duration) Option {
	return func(p *Publisher) {
		if d > 0 {
			p.threshold = d
		}
	}
}

// New constructs a Publisher. Any of bus / queries / metrics may be nil;
// when nil the corresponding ticker no-ops on each tick (so the wiring
// layer doesn't have to defensively guard the call to Start).
func New(bus *events.Bus, queries ClusterQuerier, m MetricsProvider, log *slog.Logger, opts ...Option) *Publisher {
	if log == nil {
		log = slog.Default()
	}
	p := &Publisher{
		bus:        bus,
		queries:    queries,
		metrics:    m,
		log:        log,
		tickEvery:  defaultMetricsInterval,
		sweepEvery: defaultStatusSweepInterval,
		threshold:  staleHeartbeatThreshold,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Start spawns the background tickers. Returns immediately. Cancel ctx to
// stop them; the goroutines exit on the next tick. Safe to call once.
func (p *Publisher) Start(ctx context.Context) {
	if p == nil {
		return
	}
	go p.runMetricsLoop(ctx)
	go p.runStatusSweepLoop(ctx)
}

func (p *Publisher) runMetricsLoop(ctx context.Context) {
	t := time.NewTicker(p.tickEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.publishMetrics(ctx)
		}
	}
}

func (p *Publisher) runStatusSweepLoop(ctx context.Context) {
	t := time.NewTicker(p.sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.sweepStatuses(ctx)
		}
	}
}

// publishMetrics fans out one cluster.metrics event per active cluster.
// Snapshots come from the clustermetrics provider so a tight loop here only
// hits the database and (mostly) cached snapshots — actual API round-trips
// happen at most once per cluster per provider TTL.
// listAllClusters pages through the entire non-decommissioned fleet. Both the
// metrics publish and the status sweep are authoritative full-fleet passes, so
// a single Limit:500,Offset:0 read silently froze every cluster past the 500th
// (its status never transitioned active<->disconnected). Page until short.
func (p *Publisher) listAllClusters(ctx context.Context) ([]sqlc.Cluster, error) {
	const pageSize = 500
	var all []sqlc.Cluster
	for offset := int32(0); ; offset += pageSize {
		page, err := p.queries.ListClusters(ctx, sqlc.ListClustersParams{Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < pageSize {
			return all, nil
		}
	}
}

func (p *Publisher) publishMetrics(ctx context.Context) {
	if p.bus == nil || p.queries == nil || p.metrics == nil {
		return
	}
	clusters, err := p.listAllClusters(ctx)
	if err != nil {
		p.log.Debug("metrics publisher: list clusters failed", slog.String("error", err.Error()))
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	publish := func(c sqlc.Cluster, snap clustermetrics.Snapshot) {
		p.bus.Publish(events.TypeClusterMetrics, map[string]any{
			"cluster_id":        c.ID.String(),
			"cpu_percentage":    snap.CPUPercentage,
			"memory_percentage": snap.MemoryPercentage,
			"pod_count":         snap.PodCount,
			"timestamp":         now,
		})
	}
	sem := make(chan struct{}, metricsFanoutConcurrency)
	var wg sync.WaitGroup
	for _, c := range clusters {
		if c.Status != "active" {
			continue
		}
		// A stalled/half-disconnected remote agent whose heartbeat is already
		// past the staleness threshold would cost the full 4s timeout for a
		// snapshot that comes back empty anyway: the provider's cache TTL (30s)
		// is far shorter than the threshold (2m), so any cached snapshot is
		// already expired and Get would pay the full round-trip. Publish a zero
		// snapshot without the round-trip so one dead agent never slows the
		// pass; the status sweep flips it to disconnected shortly.
		if !c.IsLocal && (!c.LastHeartbeat.Valid || time.Since(c.LastHeartbeat.Time) > p.threshold) {
			publish(c, clustermetrics.Snapshot{})
			continue
		}
		c := c
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			// 4s per cluster bounds a worst-case agent stall; cache hits return
			// almost instantly so the typical fetch runs in microseconds.
			mctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			snap := p.metrics.Get(mctx, c.ID.String(), c.IsLocal)
			cancel()
			publish(c, snap)
		}()
	}
	wg.Wait()
}

// sweepStatuses checks every cluster's last_heartbeat and flips the status
// column when it crosses the staleness threshold. Two transitions are
// emitted as cluster.status_changed events:
//
//   - active   -> disconnected  (heartbeat older than threshold)
//   - disconnected -> active    (heartbeat fresh again, e.g. after a flap)
//
// "error" / "warning" / "connecting" / "provisioning" are left alone so we
// don't trample human or controller-driven status writes.
func (p *Publisher) sweepStatuses(ctx context.Context) {
	if p.bus == nil || p.queries == nil {
		return
	}
	clusters, err := p.listAllClusters(ctx)
	if err != nil {
		p.log.Debug("status sweep: list clusters failed", slog.String("error", err.Error()))
		return
	}
	for _, c := range clusters {
		next := decideStatus(c, p.threshold)
		if next == "" || next == c.Status {
			continue
		}
		n, err := p.queries.UpdateClusterStatusOnHeartbeat(ctx, sqlc.UpdateClusterStatusOnHeartbeatParams{
			ID:     c.ID,
			Status: next,
		})
		if err != nil {
			p.log.Debug("status sweep: update failed",
				slog.String("cluster_id", c.ID.String()),
				slog.String("error", err.Error()),
			)
			continue
		}
		// CAS miss (0 rows): heartbeat moved under us — do not emit a false event.
		if n == 0 {
			continue
		}
		p.bus.Publish(events.TypeClusterStatusChanged, map[string]any{
			"cluster_id": c.ID.String(),
			"old_status": c.Status,
			"new_status": next,
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// decideStatus implements the active<->disconnected flip rule. Returns "" to
// mean "leave the row alone" so callers can distinguish "no change" from a
// concrete next-state without sentinel hacks. Local clusters are exempted
// from the disconnected flip — the management cluster is always reachable
// when the server is running, and its row exists primarily for UI display.
func decideStatus(c sqlc.Cluster, threshold time.Duration) string {
	if c.IsLocal {
		return ""
	}
	switch c.Status {
	case "active":
		if !c.LastHeartbeat.Valid {
			// Active without ever heart-beating is a degenerate state — a
			// stale row from before the agent connected. Treat as
			// disconnected so the UI reflects reality.
			return "disconnected"
		}
		if time.Since(c.LastHeartbeat.Time) > threshold {
			return "disconnected"
		}
	case "disconnected":
		if c.LastHeartbeat.Valid && time.Since(c.LastHeartbeat.Time) <= threshold {
			return "active"
		}
	}
	return ""
}

// Compile-time interface guards ensure *sqlc.Queries continues to satisfy
// our minimal interface even as the generated code evolves.
var _ ClusterQuerier = (*sqlc.Queries)(nil)

// Compile-time guard so refactors of clustermetrics.Provider don't silently
// break the publisher.
var _ MetricsProvider = (*clustermetrics.Provider)(nil)
