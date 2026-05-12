package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
)

// FEATURES-051126 T15 — chi-aware HTTP tracing wrapper.
//
// otelhttp.NewHandler creates one span per incoming request before chi
// routes it, so the span name defaults to the HTTP method alone (the
// raw URL is high-cardinality and useless for aggregation). To get
// chi's route pattern into the span name we install a tiny middleware
// downstream of routing that renames the span once chi.RouteContext is
// populated.
//
// Result: span name = "GET /api/v1/clusters/{id}/k8s/*" which is what
// every backend (Tempo/Jaeger/Honeycomb) expects for grouping.

// wrapWithTracing wraps the chi router so each request gets an OTel
// server span. The actual span name is upgraded by chiRoutePatternSpanName
// after chi resolves the route — see RouterDependencies wiring below.
func wrapWithTracing(h http.Handler) http.Handler {
	return otelhttp.NewHandler(h, "http.server",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			// Pre-routing name; will be replaced by the chi middleware
			// once the pattern is known. Method-only keeps cardinality
			// low for the (rare) requests that never match a route.
			return r.Method
		}),
	)
}

// chiRoutePatternSpanName is a chi middleware that renames the active
// OTel span to "METHOD /route/pattern" once chi has resolved the route.
// Mount it inside the chi router so it runs after routing but before
// the handler. The previous span name (HTTP method) is replaced in
// place; no new span is created.
func chiRoutePatternSpanName(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		// Read the pattern AFTER routing — chi populates it during
		// ServeHTTP. Doing it post-call also means we capture the
		// final pattern after any sub-router nesting.
		if rc := chi.RouteContext(r.Context()); rc != nil {
			if pattern := rc.RoutePattern(); pattern != "" {
				if span := trace.SpanFromContext(r.Context()); span.IsRecording() {
					span.SetName(r.Method + " " + pattern)
				}
			}
		}
	})
}
