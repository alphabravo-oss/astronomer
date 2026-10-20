package telemetry

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// These describe exporter calls, not process/dependency readiness or the SDK's
// internal retries and queue drops. No endpoint, error text or trace ID labels.
type tracingMetricsCollector struct {
	enabled                                   atomic.Bool
	success                                   atomic.Uint64
	failure                                   atomic.Uint64
	lastSuccess                               atomic.Int64
	enabledDesc, exportsDesc, lastSuccessDesc *prometheus.Desc
}

func newTracingMetricsCollector() *tracingMetricsCollector {
	return &tracingMetricsCollector{
		enabledDesc:     prometheus.NewDesc("astronomer_tracing_enabled", "1 when trace export is configured; not collector readiness.", observability.MetricLabels(), nil),
		exportsDesc:     prometheus.NewDesc("astronomer_tracing_exports_total", "Completed SDK export calls, including failure; not internal retries or queue drops.", observability.MetricLabels("result"), nil),
		lastSuccessDesc: prometheus.NewDesc("astronomer_tracing_last_success_timestamp_seconds", "Last successful export time; zero means no successful export observed.", observability.MetricLabels(), nil),
	}
}

func (metrics *tracingMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- metrics.enabledDesc
	ch <- metrics.exportsDesc
	ch <- metrics.lastSuccessDesc
}

func (metrics *tracingMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	enabled := 0.0
	if metrics.enabled.Load() {
		enabled = 1
	}
	// Instance identity is established after tracing initialization. Resolve it
	// at collection time rather than retaining an extra "unknown" startup series.
	ch <- prometheus.MustNewConstMetric(metrics.enabledDesc, prometheus.GaugeValue, enabled, observability.MetricValues()...)
	ch <- prometheus.MustNewConstMetric(metrics.exportsDesc, prometheus.CounterValue, float64(metrics.success.Load()), observability.MetricValues("success")...)
	ch <- prometheus.MustNewConstMetric(metrics.exportsDesc, prometheus.CounterValue, float64(metrics.failure.Load()), observability.MetricValues("failure")...)
	ch <- prometheus.MustNewConstMetric(metrics.lastSuccessDesc, prometheus.GaugeValue, float64(metrics.lastSuccess.Load())/1e9, observability.MetricValues()...)
}

var tracingMetrics = newTracingMetricsCollector()

func init() { prometheus.MustRegister(tracingMetrics) }

type tracingMetricsExporter struct {
	sdktrace.SpanExporter
	metrics *tracingMetricsCollector
}

func (exporter tracingMetricsExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := exporter.SpanExporter.ExportSpans(ctx, spans)
	if err != nil {
		exporter.metrics.failure.Add(1)
	} else {
		exporter.metrics.success.Add(1)
		exporter.metrics.lastSuccess.Store(time.Now().UnixNano())
	}
	return err
}
