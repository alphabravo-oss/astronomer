package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// contextKey is an unexported type for context keys in this package.
type contextKey string

const requestIDKey contextKey = "request_id"

// requestIDGeneratedKey records the PROVENANCE of the request id: true only
// when this server minted it, absent/false when the client supplied it.
//
// The id itself is deliberately client-influenceable (X-Correlation-Id /
// X-Request-ID are accepted verbatim so a caller can stitch its own traces to
// ours), which is fine for a log line but NOT fine for anything that travels
// as identity. See GetGeneratedRequestID.
const requestIDGeneratedKey contextKey = "request_id_generated"

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

		ctx := context.WithValue(r.Context(), requestIDKey, id)
		ctx = context.WithValue(ctx, requestIDGeneratedKey, generated)
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
//
// CAUTION: the value may have been supplied by the client (see RequestID). It
// is safe for logs and audit rows, which record what the caller claimed, but
// NOT for anything that leaves this process as identity — use
// GetGeneratedRequestID there.
func GetCorrelationID(ctx context.Context) string {
	return GetRequestID(ctx)
}

// GetGeneratedRequestID returns the request id ONLY when this server minted it,
// and "" when the client supplied X-Correlation-Id / X-Request-ID.
//
// Rationale (docs/design/downstream-impersonation.md §7 invariant 3, "the
// identity envelope is populated from the session only"): the correlation id is
// the one identity-adjacent value a client can choose. Carrying an
// attacker-chosen string in protocol.CallerIdentity.RequestID would let a caller
// forge the link between a downstream action and an arbitrary control-plane
// request — the exact forensic attribution the feature exists to deliver. An
// empty id is strictly better than a forged one, so the untrusted value is
// dropped rather than propagated.
func GetGeneratedRequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if generated, _ := ctx.Value(requestIDGeneratedKey).(bool); !generated {
		return ""
	}
	return GetRequestID(ctx)
}
