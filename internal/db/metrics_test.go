package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func TestUpdatePoolMetrics(t *testing.T) {
	prev := poolMetricsSnapshot{
		acquireCount:           10,
		emptyAcquireCount:      2,
		canceledAcquireCount:   1,
		acquireDurationSeconds: 3,
	}
	cur := poolMetricsSnapshot{
		acquiredConnections:    4,
		idleConnections:        6,
		totalConnections:       10,
		maxConnections:         25,
		acquireCount:           15,
		emptyAcquireCount:      5,
		canceledAcquireCount:   2,
		acquireDurationSeconds: 4.5,
	}

	updatePoolMetrics(poolMetricsSnapshot{}, poolMetricsSnapshot{})
	updatePoolMetrics(prev, cur)

	if got := metricValue(t, dbPoolAcquiredConnections.WithLabelValues(observability.MetricValues()...)); got != 4 {
		t.Fatalf("acquired gauge = %v, want 4", got)
	}
	if got := metricValue(t, dbPoolIdleConnections.WithLabelValues(observability.MetricValues()...)); got != 6 {
		t.Fatalf("idle gauge = %v, want 6", got)
	}
	if got := metricValue(t, dbPoolTotalConnections.WithLabelValues(observability.MetricValues()...)); got != 10 {
		t.Fatalf("total gauge = %v, want 10", got)
	}
	if got := metricValue(t, dbPoolMaxConnections.WithLabelValues(observability.MetricValues()...)); got != 25 {
		t.Fatalf("max gauge = %v, want 25", got)
	}
	if got := metricValue(t, dbPoolAcquireCountTotal.WithLabelValues(observability.MetricValues()...)); got != 5 {
		t.Fatalf("acquire counter = %v, want 5", got)
	}
	if got := metricValue(t, dbPoolEmptyAcquireCountTotal.WithLabelValues(observability.MetricValues()...)); got != 3 {
		t.Fatalf("empty acquire counter = %v, want 3", got)
	}
	if got := metricValue(t, dbPoolCanceledAcquireCountTotal.WithLabelValues(observability.MetricValues()...)); got != 1 {
		t.Fatalf("canceled acquire counter = %v, want 1", got)
	}
	if got := metricValue(t, dbPoolAcquireDurationSecondsTotal.WithLabelValues(observability.MetricValues()...)); got != 1.5 {
		t.Fatalf("acquire duration counter = %v, want 1.5", got)
	}
}

func TestClassifySQLOperation(t *testing.T) {
	tests := map[string]string{
		"SELECT 1":              "select",
		" insert into t values": "insert",
		"UPDATE t SET x = 1":    "update",
		"DELETE FROM t":         "delete",
		"WITH cte AS (...)":     "cte",
		"VACUUM":                "other",
		"":                      "other",
	}
	for sql, want := range tests {
		if got := classifySQLOperation(sql); got != want {
			t.Fatalf("classifySQLOperation(%q) = %q, want %q", sql, got, want)
		}
	}
}

func TestQueryTracerRecordsDuration(t *testing.T) {
	dbQueryDurationSeconds.Reset()

	tracer := NewQueryTracer()
	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	time.Sleep(time.Millisecond)
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	if got := histogramCount(t, "astronomer_db_query_duration_seconds", map[string]string{
		"astronomer_instance_id": observability.InstanceID(),
		"operation":              "select",
		"status":                 "success",
	}); got != 1 {
		t.Fatalf("success histogram count = %d, want 1", got)
	}

	ctx = tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "UPDATE things SET v = 1"})
	time.Sleep(time.Millisecond)
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("boom")})

	if got := histogramCount(t, "astronomer_db_query_duration_seconds", map[string]string{
		"astronomer_instance_id": observability.InstanceID(),
		"operation":              "update",
		"status":                 "error",
	}); got != 1 {
		t.Fatalf("error histogram count = %d, want 1", got)
	}
}

func metricValue(t *testing.T, collector interface{ Write(*dto.Metric) error }) float64 {
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

func histogramCount(t *testing.T, name string, labels map[string]string) uint64 {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricMatchesLabels(metric, labels) {
				if metric.Histogram == nil {
					t.Fatal("expected histogram metric")
				}
				return metric.Histogram.GetSampleCount()
			}
		}
	}
	t.Fatalf("metric %s with labels %+v not found", name, labels)
	return 0
}

func metricMatchesLabels(metric *dto.Metric, expected map[string]string) bool {
	for key, want := range expected {
		found := false
		for _, label := range metric.GetLabel() {
			if label.GetName() == key && label.GetValue() == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
