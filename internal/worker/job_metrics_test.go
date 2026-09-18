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
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

func TestInstrumentTaskRecordsSuccess(t *testing.T) {
	workerJobsTotal.Reset()
	workerJobDurationSeconds.Reset()
	workerJobRetryAttemptsTotal.Reset()

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

func TestInstrumentTaskLogsTraceID(t *testing.T) {
	workerJobsTotal.Reset()
	workerJobDurationSeconds.Reset()
	workerJobRetryAttemptsTotal.Reset()

	previousProvider := otel.GetTracerProvider()
	provider := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(provider)
	defer func() {
		otel.SetTracerProvider(previousProvider)
		_ = provider.Shutdown(context.Background())
	}()

	ctx, span := otel.Tracer("test").Start(context.Background(), "parent")
	traceID := trace.SpanContextFromContext(ctx).TraceID().String()
	defer span.End()

	var buf bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(previousLogger)

	handler := instrumentTask("job.trace", func(context.Context, *asynq.Task) error {
		return nil
	})
	if err := handler(ctx, asynq.NewTask("job.trace", nil)); err != nil {
		t.Fatalf("handler error = %v", err)
	}

	lines := splitJSONLines(t, buf.Bytes())
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d", len(lines))
	}
	for _, line := range lines {
		if line["trace_id"] != traceID {
			t.Fatalf("trace_id = %v, want %s in line %#v", line["trace_id"], traceID, line)
		}
	}
	metric := &dto.Metric{}
	if err := workerJobsTotal.WithLabelValues(observability.MetricValues("job.trace", "success")...).Write(metric); err != nil {
		t.Fatal(err)
	}
	exemplar := metric.GetCounter().GetExemplar()
	if exemplar == nil {
		t.Fatal("worker counter is missing sampled trace exemplar")
	}
	var exemplarTraceID string
	for _, label := range exemplar.GetLabel() {
		if label.GetName() == "trace_id" {
			exemplarTraceID = label.GetValue()
		}
	}
	if exemplarTraceID != traceID {
		t.Fatalf("metric exemplar trace_id = %q, want %q", exemplarTraceID, traceID)
	}
}

func TestInstrumentTaskLogsPayloadIdentifiers(t *testing.T) {
	workerJobsTotal.Reset()
	workerJobDurationSeconds.Reset()
	workerJobRetryAttemptsTotal.Reset()

	var buf bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(previousLogger)

	payload := []byte(`{"actor_user_id":"user-123","cluster_id":"cluster-123","operation_id":"op-456"}`)
	handler := instrumentTask("job.identifiers", func(context.Context, *asynq.Task) error {
		return nil
	})
	if err := handler(context.Background(), asynq.NewTask("job.identifiers", payload)); err != nil {
		t.Fatalf("handler error = %v", err)
	}

	lines := splitJSONLines(t, buf.Bytes())
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d", len(lines))
	}
	for _, line := range lines {
		if line["actor_id"] != "user-123" {
			t.Fatalf("actor_id = %v, want user-123 in line %#v", line["actor_id"], line)
		}
		if line["cluster_id"] != "cluster-123" {
			t.Fatalf("cluster_id = %v, want cluster-123 in line %#v", line["cluster_id"], line)
		}
		if line["operation_id"] != "op-456" {
			t.Fatalf("operation_id = %v, want op-456 in line %#v", line["operation_id"], line)
		}
	}
}

func TestInstrumentTaskPropagatesCorrelationIntoFollowUpContext(t *testing.T) {
	const correlationID = "request-correlation-123"
	var got string
	handler := instrumentTask("job.correlation", func(ctx context.Context, _ *asynq.Task) error {
		got = reqctx.CorrelationID(ctx)
		return nil
	})
	if err := handler(context.Background(), asynq.NewTask("job.correlation", []byte(`{"_correlation_id":"request-correlation-123"}`))); err != nil {
		t.Fatal(err)
	}
	if got != correlationID {
		t.Fatalf("correlation ID in worker context = %q, want %q", got, correlationID)
	}
}

func TestInstrumentTaskRecordsError(t *testing.T) {
	workerJobsTotal.Reset()
	workerJobDurationSeconds.Reset()
	workerJobRetryAttemptsTotal.Reset()

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
