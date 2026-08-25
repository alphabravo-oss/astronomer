package middleware

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultMaxRequestBodyBytes int64 = 32 << 20
	DefaultRequestBodyTimeout        = 30 * time.Second
)

// BoundRequestBodies closes the slow-body gap left intentionally by the
// server-wide zero ReadTimeout required for WebSocket and SSE connections.
// Only methods that may carry a REST body receive a socket read deadline and
// MaxBytesReader; long-lived GET streams and protocol upgrades are untouched.
func BoundRequestBodies(maximum int64, timeout time.Duration) func(http.Handler) http.Handler {
	if maximum <= 0 {
		maximum = DefaultMaxRequestBodyBytes
	}
	if timeout <= 0 {
		timeout = DefaultRequestBodyTimeout
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !requestMayCarryBody(r.Method) || isProtocolUpgrade(r) {
				next.ServeHTTP(w, r)
				return
			}
			if r.ContentLength > maximum {
				errorObject := map[string]string{
					"code": "request_body_too_large", "message": "Request body exceeds the configured limit",
				}
				if requestID := GetRequestID(r.Context()); requestID != "" {
					errorObject["request_id"] = requestID
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": errorObject})
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maximum)
			controller := http.NewResponseController(w)
			deadlineSet := controller.SetReadDeadline(time.Now().Add(timeout)) == nil
			if deadlineSet {
				defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
			}
			next.ServeHTTP(w, r)
		})
	}
}

func requestMayCarryBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func isProtocolUpgrade(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") ||
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}
