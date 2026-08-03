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

// argocdContentSecurityPolicy is the CSP for the reverse-proxied ArgoCD UI under
// /argocd/*. Argo's SPA ships an inline bootstrap <script> (theme init + webpack
// runtime), so the default script-src 'self' blocks it and the UI never loads.
// This keeps every other hardening directive (object-src 'none', base-uri
// 'self', img/style/connect rules) and only relaxes script-src to allow inline,
// plus frame-ancestors 'self' so the app can render (vs the default 'none').
const argocdContentSecurityPolicy = "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self' ws: wss:"

// isArgoCDUIPath reports whether the request targets the embedded ArgoCD UI
// reverse proxy (top-level /argocd or /argocd/*).
func isArgoCDUIPath(p string) bool {
	return p == "/argocd" || strings.HasPrefix(p, "/argocd/")
}

// SecurityHeaders adds browser hardening headers for API and proxied UI
// responses. It deliberately avoids overriding handler-provided values so
// narrowly-scoped proxies can loosen a header later if a downstream UI needs
// it.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		setHeaderIfEmpty(h, "X-Content-Type-Options", "nosniff")
		setHeaderIfEmpty(h, "Referrer-Policy", "strict-origin-when-cross-origin")
		csp := defaultContentSecurityPolicy
		if isArgoCDUIPath(r.URL.Path) {
			// The ArgoCD UI needs inline scripts + self-framing; frame-ancestors
			// in its CSP governs framing, so X-Frame-Options aligns to SAMEORIGIN
			// rather than the default DENY.
			csp = argocdContentSecurityPolicy
			setHeaderIfEmpty(h, "X-Frame-Options", "SAMEORIGIN")
		} else {
			setHeaderIfEmpty(h, "X-Frame-Options", "DENY")
		}
		setHeaderIfEmpty(h, "Content-Security-Policy", csp)
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
