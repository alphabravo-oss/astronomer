package observability

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
)

// IncrementWithTraceExemplar records a counter observation and, when the
// current span is sampled, links that observation to its trace. Trace IDs are
// exemplars rather than metric labels so they do not create an unbounded label
// cardinality.
func IncrementWithTraceExemplar(ctx context.Context, counter prometheus.Counter) {
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() && spanContext.IsSampled() {
		if exemplar, ok := counter.(prometheus.ExemplarAdder); ok {
			exemplar.AddWithExemplar(1, prometheus.Labels{"trace_id": spanContext.TraceID().String()})
			return
		}
	}
	counter.Inc()
}

// ObserveWithTraceExemplar records an observation and links it to the current
// sampled trace when the collector supports Prometheus exemplars.
func ObserveWithTraceExemplar(ctx context.Context, observer prometheus.Observer, value float64) {
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() && spanContext.IsSampled() {
		if exemplar, ok := observer.(prometheus.ExemplarObserver); ok {
			exemplar.ObserveWithExemplar(value, prometheus.Labels{"trace_id": spanContext.TraceID().String()})
			return
		}
	}
	observer.Observe(value)
}
