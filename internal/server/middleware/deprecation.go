package middleware

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// DeprecatedRoute emits machine-readable RFC 8594-compatible lifecycle
// headers while preserving the legacy handler's status and body contract.
func DeprecatedRoute(sunset time.Time, successor string) func(http.Handler) http.Handler {
	successor = strings.TrimSpace(successor)
	if sunset.IsZero() || !strings.HasPrefix(successor, "/") || strings.ContainsAny(successor, "\r\n<>") {
		panic("deprecated route requires a fixed sunset and safe absolute-path successor")
	}
	sunsetHeader := sunset.UTC().Format(http.TimeFormat)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resolvedSuccessor := successor
			if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
				for index, key := range routeContext.URLParams.Keys {
					if index < len(routeContext.URLParams.Values) {
						resolvedSuccessor = strings.ReplaceAll(resolvedSuccessor, "{"+key+"}", url.PathEscape(routeContext.URLParams.Values[index]))
					}
				}
			}
			w.Header().Set("Deprecation", "true")
			w.Header().Set("Sunset", sunsetHeader)
			w.Header().Set("Link", "<"+resolvedSuccessor+">; rel=\"successor-version\"")
			next.ServeHTTP(w, r)
		})
	}
}
