package observability

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func TestProductionJSONHandlerRedactsSensitiveDiagnostics(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewJSONHandler(&output, nil)).With("operation_id", "safe-operation", "actor_id", "safe-actor")
	logger.WithGroup("credentials").Info("rejected", "name", "synthetic-group-secret")
	logger.Info("Bearer synthetic-bearer rejected", "cookie", "synthetic-cookie", "manifest", "synthetic-manifest", "private_key", "synthetic-private-key", "payload", map[string]any{"value": "synthetic-payload"}, "err", errors.New("postgres://user:synthetic-db-secret@localhost/db\npassword=synthetic-error-secret\n-----BEGIN PRIVATE KEY-----\nsynthetic-pem\n-----END PRIVATE KEY-----"), "unknown_object", struct{ Value string }{"synthetic-object"}, "token_kind", "refresh", "state", "failed")
	raw := output.String()
	for _, secret := range []string{"synthetic-group-secret", "synthetic-bearer", "synthetic-cookie", "synthetic-manifest", "synthetic-private-key", "synthetic-payload", "synthetic-db-secret", "synthetic-error-secret", "synthetic-pem", "synthetic-object"} {
		if strings.Contains(raw, secret) {
			t.Errorf("sensitive diagnostic escaped: %s", secret)
		}
	}
	for _, safe := range []string{"safe-operation", "safe-actor", "refresh", "failed"} {
		if !strings.Contains(raw, safe) {
			t.Errorf("safe correlation/category lost: %s", safe)
		}
	}
}

func TestOTLPActualWireRedactsSecretsAcrossSpanFields(t *testing.T) {
	preserveTracingGlobals(t)
	received := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		received <- raw
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	shutdown, err := InitTracing(context.Background(), slog.New(NewJSONHandler(io.Discard, nil)), TracingConfig{Endpoint: server.URL, SamplerRatio: 1, ServiceName: "safe-service"})
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("safe-scope").Start(context.Background(), "safe-operation", trace.WithAttributes(attribute.String("authorization", "synthetic-authorization"), attribute.String("db.statement", "SELECT 'synthetic-sql-literal'"), attribute.String("exception.message", "synthetic-raw-error-payload"), attribute.String("error.detail", "postgres://user:synthetic-trace-password@host/db"), attribute.StringSlice("details", []string{"Bearer synthetic-array-secret", "safe-category"})))
	span.AddEvent("Bearer synthetic-event-secret", trace.WithAttributes(attribute.String("manifest", "synthetic-event-manifest")))
	span.SetStatus(codes.Error, "password=synthetic-status-secret")
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	raw := <-received
	for _, secret := range []string{"synthetic-authorization", "synthetic-sql-literal", "synthetic-trace-password", "synthetic-array-secret", "synthetic-event-secret", "synthetic-event-manifest", "synthetic-status-secret", "synthetic-raw-error-payload"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("secret escaped actual OTLP serialization: %s", secret)
		}
	}
	for _, safe := range []string{"safe-service", "safe-operation", "safe-category"} {
		if !bytes.Contains(raw, []byte(safe)) {
			t.Errorf("safe span field lost: %s", safe)
		}
	}
}
