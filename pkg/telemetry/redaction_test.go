package telemetry

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/rs/zerolog"

	"github.com/alphabravocompany/astronomer-go/pkg/logging"
)

func TestOTLPActualWireRedactsSecretsAcrossSpanFields(t *testing.T) {
	preserveTracingGlobals(t)
	received := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		received <- raw
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	shutdown, err := initForTest(t, testLogger(io.Discard), Config{Endpoint: server.URL, Protocol: ProtocolHTTPProtobuf, SampleRate: 1, ServiceName: "safe-service", ServiceVersion: "Bearer synthetic-resource-secret"})
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("safe-scope").Start(context.Background(), "safe-operation", trace.WithLinks(trace.Link{SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}}), Attributes: []attribute.KeyValue{attribute.String("cookie", "synthetic-link-secret"), attribute.String("link.kind", "safe-link")}}), trace.WithAttributes(attribute.String("authorization", "synthetic-authorization"), attribute.String("db.statement", "SELECT 'synthetic-sql-literal'"), attribute.String("exception.message", "synthetic-raw-error-payload"), attribute.String("error.detail", "postgres://user:synthetic-trace-password@host/db"), attribute.StringSlice("details", []string{"Bearer synthetic-array-secret", "safe-category"})))
	span.AddEvent("Bearer synthetic-event-secret", trace.WithAttributes(attribute.String("manifest", "synthetic-event-manifest")))
	span.SetStatus(codes.Error, "password=synthetic-status-secret")
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	select {
	case raw = <-received:
	case <-ctx.Done():
		t.Fatal("OTLP request not received within shutdown budget")
	}
	for _, secret := range []string{"synthetic-authorization", "synthetic-sql-literal", "synthetic-trace-password", "synthetic-array-secret", "synthetic-event-secret", "synthetic-event-manifest", "synthetic-status-secret", "synthetic-raw-error-payload", "synthetic-resource-secret", "synthetic-link-secret"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("secret escaped actual OTLP serialization: %s", secret)
		}
	}
	for _, safe := range []string{"safe-service", "safe-operation", "safe-category", "safe-link"} {
		if !bytes.Contains(raw, []byte(safe)) {
			t.Errorf("safe span field lost: %s", safe)
		}
	}
}

func testLogger(w io.Writer) zerolog.Logger {
	logger, err := logging.NewWithWriter(w, logging.Config{Level: "debug"})
	if err != nil {
		panic(err)
	}
	return logger
}
