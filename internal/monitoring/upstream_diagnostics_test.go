package monitoring

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/logging"
)

func TestMonitoringUpstreamBodiesAbsentFromJSONAndActualOTLP(t *testing.T) {

	const secret = "synthetic-unlabelled-upstream-response-secret"
	received := make(chan []byte, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		received <- body
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer collector.Close()
	priorProvider, priorPropagator := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	defer func() { otel.SetTracerProvider(priorProvider); otel.SetTextMapPropagator(priorPropagator) }()
	var output bytes.Buffer
	logger := diagnosticSlog(&output)
	shutdown, err := observability.InitTracing(context.Background(), logger, observability.TracingConfig{Protocol: observability.ProtocolHTTPProtobuf, Endpoint: collector.URL, SamplerRatio: 1, ServiceName: "upstream-error-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") == "vector(2)" {
			_, _ = io.WriteString(w, `{"status":"error","error":"`+secret+`"}`)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, secret)
	}))
	defer backend.Close()

	client := &Client{baseURL: backend.URL, httpClient: backend.Client()}
	for _, operation := range []string{"health", "query", "application-error"} {
		ctx, span := otel.Tracer("monitoring-upstream").Start(context.Background(), operation)
		var err error
		if operation == "health" {
			err = client.HealthCheck(ctx)
		} else {
			query := "vector(1)"
			if operation == "application-error" {
				query = "vector(2)"
			}
			_, err = client.QueryScalar(ctx, query)
		}
		if err == nil {
			t.Fatal("upstream failure accepted")
		}
		logger.Error("upstream rejected", "operation", operation, "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
	}

	budget, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := shutdown(budget); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-received:
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("raw upstream body entered actual OTLP payload")
		}
	case <-budget.Done():
		t.Fatal("OTLP request absent within budget")
	}
	if strings.Contains(output.String(), secret) {
		t.Fatal("raw upstream body entered final JSON logger")
	}
	if !strings.Contains(output.String(), "503") {
		t.Fatal("safe HTTP status lost")
	}
}

func diagnosticSlog(w io.Writer) *slog.Logger {
	logger, err := logging.NewWithWriter(w, logging.Config{Level: "debug"})
	if err != nil {
		panic(err)
	}
	return logging.Slog(logger)
}
