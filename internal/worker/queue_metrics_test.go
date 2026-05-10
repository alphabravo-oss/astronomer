package worker

import (
	"testing"
	"time"

	"github.com/hibiken/asynq"
	dto "github.com/prometheus/client_model/go"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func TestUpdateQueueMetrics(t *testing.T) {
	workerQueueDepth.Reset()
	workerQueueLatencySeconds.Reset()

	updateQueueMetrics([]*asynq.QueueInfo{
		{
			Queue:       "critical",
			Size:        9,
			Pending:     3,
			Active:      2,
			Scheduled:   1,
			Retry:       1,
			Archived:    1,
			Completed:   1,
			Aggregating: 0,
			Latency:     42 * time.Second,
		},
	})

	if got := metricValue(t, workerQueueDepth.WithLabelValues(observability.MetricValues("critical", "pending")...)); got != 3 {
		t.Fatalf("pending depth = %v, want 3", got)
	}
	if got := metricValue(t, workerQueueDepth.WithLabelValues(observability.MetricValues("critical", "total")...)); got != 9 {
		t.Fatalf("total depth = %v, want 9", got)
	}
	if got := metricValue(t, workerQueueLatencySeconds.WithLabelValues(observability.MetricValues("critical")...)); got != 42 {
		t.Fatalf("latency = %v, want 42", got)
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
