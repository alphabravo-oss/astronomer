package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/hibiken/asynq"
	dto "github.com/prometheus/client_model/go"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func TestInstrumentTaskRecordsSuccess(t *testing.T) {
	workerJobsTotal.Reset()
	workerJobDurationSeconds.Reset()

	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(previous)

	handler := instrumentTask("job.success", func(context.Context, *asynq.Task) error {
		return nil
	})
	if err := handler(context.Background(), asynq.NewTask("job.success", nil)); err != nil {
		t.Fatalf("handler error = %v", err)
	}

	if got := counterValue(t, workerJobsTotal.WithLabelValues(observability.MetricValues("job.success", "success")...)); got != 1 {
		t.Fatalf("success counter = %v, want 1", got)
	}

	lines := splitJSONLines(t, buf.Bytes())
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d", len(lines))
	}
	if lines[0]["event"] != "worker_job_started" {
		t.Fatalf("first event = %v, want worker_job_started", lines[0]["event"])
	}
	if lines[1]["event"] != "worker_job_completed" {
		t.Fatalf("second event = %v, want worker_job_completed", lines[1]["event"])
	}
	if lines[1]["status"] != "success" {
		t.Fatalf("completion status = %v, want success", lines[1]["status"])
	}
}

func TestInstrumentTaskRecordsError(t *testing.T) {
	workerJobsTotal.Reset()
	workerJobDurationSeconds.Reset()

	handler := instrumentTask("job.error", func(context.Context, *asynq.Task) error {
		return errors.New("boom")
	})
	if err := handler(context.Background(), asynq.NewTask("job.error", nil)); err == nil {
		t.Fatal("expected error")
	}

	if got := counterValue(t, workerJobsTotal.WithLabelValues(observability.MetricValues("job.error", "error")...)); got != 1 {
		t.Fatalf("error counter = %v, want 1", got)
	}
}

func counterValue(t *testing.T, collector interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := collector.Write(m); err != nil {
		t.Fatalf("collector.Write(): %v", err)
	}
	if m.Counter == nil || m.Counter.Value == nil {
		t.Fatal("counter value missing")
	}
	return m.Counter.GetValue()
}

func splitJSONLines(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	raw := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	out := make([]map[string]any, 0, len(raw))
	for _, line := range raw {
		if len(line) == 0 {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(line, &payload); err != nil {
			t.Fatalf("unmarshal log line: %v", err)
		}
		out = append(out, payload)
	}
	return out
}
