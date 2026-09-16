package middleware

import (
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/google/uuid"
)

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
		generated := false
		if id == "" || !isValidRequestID(id) {
			id = uuid.New().String()
			generated = true
		}

		ctx := reqctx.WithRequestID(r.Context(), id, generated)
		w.Header().Set(requestIDHeader, id)
		w.Header().Set(correlationIDHeader, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
