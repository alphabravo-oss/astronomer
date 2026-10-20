package logging

import (
	"context"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"
)

// Field names shared by every binary.
const (
	ComponentField     = "component"
	CorrelationIDField = "correlation_id"
	TraceIDField       = "trace_id"
)

// Inbound correlation headers, in precedence order after traceparent.
const (
	TraceparentHeader   = "Traceparent"
	CorrelationIDHeader = "X-Correlation-Id"
	RequestIDHeader     = "X-Request-Id"
)

const maxCorrelationIDLen = 256

type correlationKey struct{}

// WithCorrelationID returns ctx carrying the request correlation id.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, correlationKey{}, id)
}

// CorrelationID returns the correlation id carried by ctx, or "".
func CorrelationID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(correlationKey{}).(string)
	return id
}

// Ctx derives a request-scoped logger from base: it carries base's component
// field plus correlation_id from ctx and trace_id of the active span.
// It returns a pointer so call sites can chain Ctx(ctx, l).Info().
func Ctx(ctx context.Context, base zerolog.Logger) *zerolog.Logger {
	builder := base.With()
	if id := CorrelationID(ctx); id != "" {
		builder = builder.Str(CorrelationIDField, id)
	}
	if span := trace.SpanContextFromContext(ctx); span.HasTraceID() {
		builder = builder.Str(TraceIDField, span.TraceID().String())
	}
	logger := builder.Logger()
	return &logger
}

// contextHook adds correlation fields to events that carry a context but were
// not built from Ctx — notably every slog *Context call routed through
// SlogHandler, which propagates the slog context onto the zerolog event.
type contextHook struct{}

func (contextHook) Run(event *zerolog.Event, _ zerolog.Level, _ string) {
	ctx := event.GetCtx()
	if ctx == nil || ctx == context.Background() {
		return
	}
	if id := CorrelationID(ctx); id != "" {
		event.Str(CorrelationIDField, id)
	}
	if span := trace.SpanContextFromContext(ctx); span.HasTraceID() {
		event.Str(TraceIDField, span.TraceID().String())
	}
}

// InboundCorrelationID chooses the correlation id for an inbound request:
// the W3C traceparent trace-id when the header is valid, otherwise a valid
// X-Correlation-ID, otherwise a valid X-Request-ID. ok is false when the
// caller must mint a new id.
func InboundCorrelationID(header http.Header) (id string, ok bool) {
	if traceID, valid := TraceIDFromTraceparent(header.Get(TraceparentHeader)); valid {
		return traceID, true
	}
	for _, name := range []string{CorrelationIDHeader, RequestIDHeader} {
		if value := strings.TrimSpace(header.Get(name)); validCorrelationID(value) {
			return value, true
		}
	}
	return "", false
}

// TraceIDFromTraceparent extracts the 32-hex trace-id from a W3C traceparent
// value ("00-<trace-id>-<parent-id>-<flags>").
func TraceIDFromTraceparent(value string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) < 4 || len(parts[0]) != 2 || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return "", false
	}
	if parts[0] == "ff" || (parts[0] == "00" && len(parts) != 4) {
		return "", false
	}
	for _, part := range parts[:4] {
		if !lowerHex(part) {
			return "", false
		}
	}
	if strings.Trim(parts[1], "0") == "" || strings.Trim(parts[2], "0") == "" {
		return "", false
	}
	return parts[1], true
}

func lowerHex(value string) bool {
	if _, err := hex.DecodeString(value); err != nil {
		return false
	}
	return strings.ToLower(value) == value
}

// validCorrelationID bounds client-supplied ids and rejects control
// characters, preventing log injection.
func validCorrelationID(id string) bool {
	if id == "" || len(id) > maxCorrelationIDLen {
		return false
	}
	for _, c := range id {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}
