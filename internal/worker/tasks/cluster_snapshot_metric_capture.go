package tasks

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Captured by the composition before external work. Implementations re-read
// the exact captured generation before recording; these are not auth claims.
type SnapshotMetricRecorder interface {
	Outcome(context.Context, string)
	InFlight(context.Context, float64)
}

func captureSnapshotMetrics(ctx context.Context, deps ClusterSnapshotDeps, id uuid.UUID) SnapshotMetricRecorder {
	if deps.CaptureMetrics == nil || ctx == nil || ctx.Err() != nil {
		return nil
	}
	return deps.CaptureMetrics(ctx, id)
}

type snapshotMetricGaugeOwners struct {
	serial    atomic.Uint64
	publish   chan struct{}
	published uint64
	rows      map[string]SnapshotMetricRecorder
}

func newSnapshotMetricGaugeOwners() *snapshotMetricGaugeOwners {
	return &snapshotMetricGaugeOwners{publish: make(chan struct{}, 1), rows: make(map[string]SnapshotMetricRecorder)}
}

var capturedSnapshotGauges = newSnapshotMetricGaugeOwners()

// The existing bounded poll batch supplies at most200 IDs. Prior zeroing uses
// the previously captured handle, not a fresh capture for a possibly reused ID.
// A failed pending-row query never calls this function or clears prior state.
func (deps ClusterSnapshotDeps) snapshotMetricOwners() *snapshotMetricGaugeOwners {
	if deps.metricGaugeOwners != nil {
		return deps.metricGaugeOwners
	}
	return capturedSnapshotGauges
}
func updateCapturedInFlightSnapshotGauges(ctx context.Context, deps ClusterSnapshotDeps, counts map[string]int, observation uint64) {
	deps.snapshotMetricOwners().update(ctx, deps, counts, observation)
}
func (owners *snapshotMetricGaugeOwners) update(ctx context.Context, deps ClusterSnapshotDeps, counts map[string]int, watermark uint64) {
	if deps.CaptureMetrics == nil {
		updateInFlightSnapshotGauges(counts)
		return
	}
	if ctx == nil || ctx.Err() != nil || watermark == 0 || len(counts) > int(snapshotPollBatchSize) {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx = bounded
	next := make(map[string]SnapshotMetricRecorder, len(counts))
	for raw, count := range counts {
		if ctx.Err() != nil {
			return
		}
		id, err := uuid.Parse(raw)
		if err != nil || count < 0 {
			return
		}
		metric := captureSnapshotMetrics(ctx, deps, id)
		if metric == nil {
			// An incomplete capture cannot replace remembered handles: a later
			// empty batch must still zero the originally captured generation.
			return
		}
		next[raw] = metric
	}
	select {
	case owners.publish <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-owners.publish }()
	if ctx.Err() != nil || watermark <= owners.published {
		return
	}
	owners.published = watermark
	old := owners.rows
	owners.rows = next
	for id, metric := range next {
		if metric != nil {
			metric.InFlight(ctx, float64(counts[id]))
		}
	}
	for id, metric := range old {
		if _, exists := next[id]; !exists && metric != nil {
			metric.InFlight(ctx, 0)
		}
	}
}
