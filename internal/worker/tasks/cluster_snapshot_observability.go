package tasks

import (
	"context"
	"errors"
	"log/slog"
)

// recordSnapshotOutcome is the worker-side hook into the handler's
// cluster_snapshots_total Counter. The handler package wires it at startup to
// avoid a tasks → handler import cycle.
var recordSnapshotOutcome = func(clusterID, outcome string) {}

// SetSnapshotOutcomeRecorder swaps the metric callback.
func SetSnapshotOutcomeRecorder(fn func(clusterID, outcome string)) {
	if fn != nil {
		recordSnapshotOutcome = fn
	}
}

// setInFlightSnapshotGauge is wired to the handler's
// cluster_snapshots_in_flight GaugeVec at startup.
var setInFlightSnapshotGauge = func(clusterID string, count float64) {}

// SetInFlightSnapshotGaugeSetter swaps the in-flight gauge callback.
func SetInFlightSnapshotGaugeSetter(fn func(clusterID string, count float64)) {
	if fn != nil {
		setInFlightSnapshotGauge = fn
	}
}

func logSnapshotErr(log *slog.Logger, msg string, err error) {
	if err == nil || log == nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	log.Warn("cluster snapshot worker", "phase", msg, "error", err.Error())
}
