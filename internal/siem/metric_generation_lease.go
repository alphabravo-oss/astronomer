package siem

import (
	"context"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

type MetricLease struct {
	lease observability.MetricGenerationLease
}

func CaptureMetricLease(ctx context.Context, source any, id uuid.UUID) MetricLease {
	return MetricLease{observability.CaptureMetricGeneration(ctx, source, observability.MetricSIEMForwarder, id)}
}
func init() {
	observability.RegisterMetricGenerationRetirement(observability.MetricSIEMForwarder, func(instance, id string) {
		labels := prometheus.Labels{"astronomer_instance_id": instance, "forwarder": id}
		dispatchedTotal.DeletePartialMatch(labels)
		droppedTotal.DeletePartialMatch(labels)
		queueDepth.DeletePartialMatch(labels)
	})
}
func formatLabel(value string) string {
	switch value {
	case "":
		return "rfc5424"
	case "rfc5424", "rfc3164", "cef", "ndjson":
		return value
	default:
		return "other"
	}
}
func outcomeLabel(value string) string {
	switch value {
	case "delivered", "failed":
		return value
	default:
		return "other"
	}
}
func reasonLabel(value string) string {
	switch value {
	case "tap_buffer_full", "queue_full", "retries_exhausted", "format_error":
		return value
	default:
		return "other"
	}
}
func (metric MetricLease) Dispatched(ctx context.Context, format, outcome string, n int) {
	if n <= 0 {
		return
	}
	metric.lease.Record(ctx, func(instance, id string) {
		dispatchedTotal.WithLabelValues(instance, id, formatLabel(format), outcomeLabel(outcome)).Add(float64(n))
	})
}
func (metric MetricLease) Dropped(ctx context.Context, reason string, n int) {
	if n <= 0 {
		return
	}
	metric.lease.Record(ctx, func(instance, id string) {
		droppedTotal.WithLabelValues(instance, id, reasonLabel(reason)).Add(float64(n))
	})
}
func (metric MetricLease) QueueDepth(ctx context.Context, depth int) {
	metric.lease.Record(ctx, func(instance, id string) { queueDepth.WithLabelValues(instance, id).Set(float64(max(0, depth))) })
}
