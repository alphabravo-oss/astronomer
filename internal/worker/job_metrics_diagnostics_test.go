package worker

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func TestInstrumentTaskPreservesTypedErrorRedactionAndCorrelation(t *testing.T) {
	const rawValue = "synthetic-unlabelled-sensitive-value"
	postgres := &pgconn.PgError{Code: "23505", Message: "duplicate value " + rawValue, Detail: rawValue, Where: rawValue}
	kubernetes := &apierrors.StatusError{ErrStatus: metav1.Status{
		Code: 422, Reason: metav1.StatusReasonInvalid, Message: "Secret rejected: " + rawValue,
		Details: &metav1.StatusDetails{Kind: "Secret", Name: rawValue, Causes: []metav1.StatusCause{{Type: metav1.CauseTypeFieldValueInvalid, Message: rawValue, Field: "data.value"}}},
	}}
	cases := []struct {
		name     string
		err      error
		category string
	}{
		{name: "postgres", err: postgres, category: "postgres"},
		{name: "wrapped_postgres", err: fmt.Errorf("attempt rejected: %w", postgres), category: "postgres"},
		{name: "kubernetes", err: kubernetes, category: "kubernetes"},
		{name: "wrapped_kubernetes", err: fmt.Errorf("attempt rejected: %w", kubernetes), category: "kubernetes"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(observability.NewJSONHandler(&output, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			parent := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled})
			ctx := trace.ContextWithSpanContext(context.Background(), parent)
			payload := []byte(`{"_correlation_id":"safe-task-request","actor_user_id":"safe-actor","cluster_id":"safe-cluster","operation_id":"safe-operation"}`)
			run := instrumentTask("job.diagnostic", func(context.Context, *asynq.Task) error { return test.err })
			if got := run(ctx, asynq.NewTask("job.diagnostic", payload)); got != test.err {
				t.Fatal("instrumentation changed the returned application error")
			}
			if strings.Contains(output.String(), rawValue) {
				t.Error("typed server payload escaped shipped worker diagnostic handler")
			}
			lines := splitJSONLines(t, output.Bytes())
			if len(lines) != 3 || lines[1]["event"] != "worker_job_failed" || lines[2]["status"] != "error" {
				t.Fatalf("failure lifecycle changed: %#v", lines)
			}
			for _, row := range lines {
				if row["correlation_id"] != "safe-task-request" || row["actor_id"] != "safe-actor" || row["cluster_id"] != "safe-cluster" || row["operation_id"] != "safe-operation" || row["trace_id"] != parent.TraceID().String() {
					t.Fatalf("safe task correlation/category lost: %#v", row)
				}
			}
			diagnostic, ok := lines[1]["error"].(map[string]any)
			if !ok || diagnostic["type"] != test.category {
				t.Fatalf("safe typed failure category missing: %#v", lines[1]["error"])
			}
			if test.category == "postgres" && diagnostic["sqlstate"] != "23505" {
				t.Fatalf("safe SQLSTATE missing: %#v", diagnostic)
			}
			if test.category == "kubernetes" && (diagnostic["http_status"] != float64(422) || diagnostic["reason"] != "Invalid") {
				t.Fatalf("safe Kubernetes status missing: %#v", diagnostic)
			}
		})
	}
}
