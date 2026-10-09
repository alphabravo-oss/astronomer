package observability

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	shutdown, err := InitTracing(context.Background(), slog.New(NewJSONHandler(io.Discard, nil)), TracingConfig{Endpoint: server.URL, SamplerRatio: 1, ServiceName: "safe-service", ServiceVersion: "Bearer synthetic-resource-secret"})
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

func TestWrappedDatabaseAndKubernetesErrorsOmitServerPayload(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewJSONHandler(&output, nil))
	logger.Error("write failed", "err", fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "23505", Message: "synthetic-pg-message", Detail: "Key(token)=(synthetic-pg-detail) already exists", Where: "synthetic-pg-where"}))
	logger.Error("apply failed", "err", fmt.Errorf("wrapped: %w", &apierrors.StatusError{ErrStatus: metav1.Status{Code: 422, Reason: metav1.StatusReasonInvalid, Message: "synthetic-kube-manifest", Details: &metav1.StatusDetails{Name: "synthetic-kube-details"}}}))
	for _, secret := range []string{"synthetic-pg-message", "synthetic-pg-detail", "synthetic-pg-where", "synthetic-kube-manifest", "synthetic-kube-details"} {
		if strings.Contains(output.String(), secret) {
			t.Errorf("server payload exported: %s", secret)
		}
	}
	for _, safe := range []string{"23505", "422", "postgres", "kubernetes", "Invalid"} {
		if !strings.Contains(output.String(), safe) {
			t.Errorf("safe error category absent: %s", safe)
		}
	}
}
