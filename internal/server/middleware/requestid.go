package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// contextKey is an unexported type for context keys in this package.
type contextKey string

const requestIDKey contextKey = "request_id"

const (
	requestIDHeader     = "X-Request-ID"
	correlationIDHeader = "X-Correlation-Id"
)

// maxRequestIDLen is the maximum allowed length for an X-Request-ID header value.
const maxRequestIDLen = 256

// isValidRequestID checks that the request ID is within the length cap and
// contains no control characters. This prevents log injection attacks.
func isValidRequestID(id string) bool {
	if len(id) > maxRequestIDLen {
		return false
	}
	for _, c := range id {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// RequestID prefers an incoming X-Correlation-Id header, then X-Request-ID.
// If neither header is present and valid it generates a new UUID. The shared
// identifier is stored in the request context and echoed back in both response
// headers so downstream systems can use either convention.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(correlationIDHeader)
		if id == "" {
			id = r.Header.Get(requestIDHeader)
		}
		if id == "" || !isValidRequestID(id) {
			id = uuid.New().String()
		}

		ctx := context.WithValue(r.Context(), requestIDKey, id)
		w.Header().Set(requestIDHeader, id)
		w.Header().Set(correlationIDHeader, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetRequestID extracts the request ID from the context.
// Returns an empty string if no request ID is present.
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// GetCorrelationID returns the same shared request correlation identifier used
// by GetRequestID. This alias makes call sites explicit when the identifier is
// persisted for cross-service traceability rather than just local request logs.
func GetCorrelationID(ctx context.Context) string {
	return GetRequestID(ctx)
}
