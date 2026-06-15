package handler

import (
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func TestUpdateArgoCDApplicationMetrics(t *testing.T) {
	argoCDApplicationsGauge.Reset()

	updateArgoCDApplicationMetrics([]sqlc.CountArgoCDApplicationsBySyncHealthRow{
		{SyncStatus: "Synced", HealthStatus: "Healthy", AppCount: 3},
		{SyncStatus: "", HealthStatus: "", AppCount: 2},
	})

	if got := handlerMetricValue(t, argoCDApplicationsGauge.WithLabelValues(observability.MetricValues("Synced", "Healthy")...)); got != 3 {
		t.Fatalf("Synced/Healthy app count = %v, want 3", got)
	}
	if got := handlerMetricValue(t, argoCDApplicationsGauge.WithLabelValues(observability.MetricValues("Unknown", "Unknown")...)); got != 2 {
		t.Fatalf("Unknown/Unknown app count = %v, want 2", got)
	}
}

func handlerMetricValue(t *testing.T, collector interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := collector.Write(m); err != nil {
		t.Fatalf("collector.Write(): %v", err)
	}
	switch {
	case m.Gauge != nil:
		return m.Gauge.GetValue()
	case m.Counter != nil:
		return m.Counter.GetValue()
	default:
		t.Fatal("unsupported metric type")
		return 0
	}
}
