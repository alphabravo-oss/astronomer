package middleware

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/pkg/logging"
)

// RequestID establishes the request correlation id (guide ch.05): the W3C
// traceparent trace-id when the inbound header is valid, else a valid
// X-Correlation-ID, else a valid X-Request-ID, else a new UUID. The id is
// stored in the request context (reqctx and pkg/logging) and echoed in both
// X-Correlation-ID and X-Request-ID response headers.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, inbound := logging.InboundCorrelationID(r.Header)
		if !inbound {
			id = uuid.New().String()
		}
		ctx := reqctx.WithRequestID(r.Context(), id, !inbound)
		ctx = logging.WithCorrelationID(ctx, id)
		w.Header().Set(logging.RequestIDHeader, id)
		w.Header().Set(logging.CorrelationIDHeader, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
