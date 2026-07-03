package middleware

import (
	"net/http"
	"strings"
)

// defaultContentSecurityPolicy hardens API + first-party responses. script-src
// deliberately omits 'unsafe-inline' so an injected inline <script> won't run;
// API responses are JSON (no scripts), and any HTML-serving handler/proxy that
// genuinely needs inline scripts (e.g. the embedded ArgoCD UI) either carries
// its own upstream CSP — preserved by the reverse proxy — or sets one first
// (SecurityHeaders only fills an empty header). style-src keeps 'unsafe-inline'
// because first-party styled components rely on it.
const defaultContentSecurityPolicy = "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self' ws: wss:"

// SecurityHeaders adds browser hardening headers for API and proxied UI
// responses. It deliberately avoids overriding handler-provided values so
// narrowly-scoped proxies can loosen a header later if a downstream UI needs
// it.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		setHeaderIfEmpty(h, "X-Content-Type-Options", "nosniff")
		setHeaderIfEmpty(h, "Referrer-Policy", "strict-origin-when-cross-origin")
		setHeaderIfEmpty(h, "X-Frame-Options", "DENY")
		setHeaderIfEmpty(h, "Content-Security-Policy", defaultContentSecurityPolicy)
		if RequestIsHTTPS(r) {
			setHeaderIfEmpty(h, "Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func setHeaderIfEmpty(h http.Header, key, value string) {
	if h.Get(key) == "" {
		h.Set(key, value)
	}
}

// RequestIsHTTPS reports whether a request arrived over HTTPS directly or via
// a trusted reverse proxy that stamped X-Forwarded-Proto=https.
func RequestIsHTTPS(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
