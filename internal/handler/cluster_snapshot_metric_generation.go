package handler

import (
	"context"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

type clusterSnapshotMetric struct {
	lease observability.MetricGenerationLease
}

func captureClusterSnapshotMetric(ctx context.Context, source any, id uuid.UUID) clusterSnapshotMetric {
	return clusterSnapshotMetric{observability.CaptureMetricGeneration(ctx, source, observability.MetricCluster, id)}
}
func init() {
	observability.RegisterMetricGenerationRetirement(observability.MetricCluster, func(instance, id string) {
		labels := prometheus.Labels{"astronomer_instance_id": instance, "cluster_id": id}
		clusterSnapshotsTotal.DeletePartialMatch(labels)
		clusterSnapshotsCreatedInFlight.DeletePartialMatch(labels)
		clusterSnapshotsInFlight.DeletePartialMatch(labels)
		veleroInstallStatus.DeletePartialMatch(labels)
	})
}
func (metric clusterSnapshotMetric) VeleroStatus(ctx context.Context, state string) {
	switch state {
	case "ready", "unavailable", "missing", "unreachable":
	default:
		state = "other"
	}
	metric.lease.Record(ctx, func(instance, id string) {
		veleroInstallStatus.DeletePartialMatch(prometheus.Labels{"astronomer_instance_id": instance, "cluster_id": id})
		value := 0.0
		if state == "ready" {
			value = 1
		}
		veleroInstallStatus.WithLabelValues(instance, id, state).Set(value)
	})
}
func (metric clusterSnapshotMetric) Created(ctx context.Context) {
	metric.lease.Record(ctx, func(instance, id string) { clusterSnapshotsCreatedInFlight.WithLabelValues(instance, id).Inc() })
}

func (metric clusterSnapshotMetric) Outcome(ctx context.Context, outcome string) {
	switch outcome {
	case "completed", "failed", "partial":
	default:
		outcome = "other"
	}
	metric.lease.Record(ctx, func(instance, id string) { clusterSnapshotsTotal.WithLabelValues(instance, id, outcome).Inc() })
}
func (metric clusterSnapshotMetric) InFlight(ctx context.Context, count float64) {
	if count < 0 {
		return
	}
	metric.lease.Record(ctx, func(instance, id string) { clusterSnapshotsInFlight.WithLabelValues(instance, id).Set(count) })
}
