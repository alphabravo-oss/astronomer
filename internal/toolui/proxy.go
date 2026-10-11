package toolui

import (
	"bytes"
	"net/http"
	"strings"
)

func (g *Gateway) serveTool(w http.ResponseWriter, r *http.Request) {
	if g.jwt == nil || g.cipher == nil || g.grants == nil || g.proxy == nil {
		http.Error(w, "Tool gateway unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Origin-Agent-Cluster", "?1")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Permissions-Policy", "document-domain=()")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// No same-origin frame restriction: only the configured console may embed.
	w.Header().Del("X-Frame-Options")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors "+g.main.String()+"; img-src 'self' data: blob:; font-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; connect-src 'self'; worker-src blob:; form-action 'self'")
	if r.Header.Get("Service-Worker") != "" || r.Header.Get("Sec-Fetch-Dest") == "serviceworker" {
		http.Error(w, "Service workers are not available on tool hosts", http.StatusForbidden)
		return
	}
	if r.URL.Path == redeemPath {
		g.redeem(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") != "https://"+r.Host {
		http.Error(w, "Cross-origin tool request denied", http.StatusForbidden)
		return
	}
	// Even safe cross-origin fetches are denied; normal navigations are allowed.
	if origin := r.Header.Get("Origin"); origin != "" && origin != "https://"+r.Host {
		http.Error(w, "Cross-origin tool request denied", http.StatusForbidden)
		return
	}
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		http.Error(w, "Open this tool from Astronomer to start a session", http.StatusUnauthorized)
		return
	}
	s, err := g.decode(r.Context(), cookie.Value, r.Host)
	if err != nil {
		http.Error(w, "Tool session expired; reopen the tool from Astronomer", http.StatusUnauthorized)
		return
	}
	upstream := r.Clone(r.Context())
	upstream.Header = r.Header.Clone()
	// The decrypted parent token is only for the dedicated server-side auth
	// chain. Existing tunnel proxies forward a narrow, credential-free allowlist.
	upstream.Header.Del("Cookie")
	upstream.Header.Set("Authorization", "Bearer "+s.Credential)
	g.proxy(&responseBoundary{ResponseWriter: w, mainOrigin: g.main.String(), toolOrigin: "https://" + s.Host, prefix: s.Target.Prefix()}, upstream, s.Target)
}

// Own all security headers even when a downstream tool attempts to replace
// them. This also strips sidecar cookies: browser auth belongs to this gateway.
type responseBoundary struct {
	http.ResponseWriter
	mainOrigin string
	toolOrigin string
	prefix     string
	written    bool
}

func (w *responseBoundary) WriteHeader(status int) {
	if w.written {
		return
	}
	w.written = true
	h := w.Header()
	h.Del("Content-Length")
	if location := h.Get("Location"); strings.HasPrefix(location, w.mainOrigin+w.prefix) {
		h.Set("Location", w.toolOrigin+strings.TrimPrefix(location, w.mainOrigin))
	}
	for name := range h {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "access-control-") || lower == "set-cookie" || lower == "service-worker-allowed" || lower == "clear-site-data" || lower == "x-frame-options" || lower == "refresh" {
			h.Del(name)
		}
	}
	h.Set("Origin-Agent-Cluster", "?1")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Permissions-Policy", "document-domain=()")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors "+w.mainOrigin+"; img-src 'self' data: blob:; font-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; connect-src 'self'; worker-src blob:; form-action 'self'")
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseBoundary) Write(b []byte) (int, error) {
	length := len(b)
	if strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") && w.Header().Get("Content-Encoding") == "" {
		b = bytes.ReplaceAll(b, []byte(w.mainOrigin+w.prefix), []byte(w.toolOrigin+w.prefix))
	}
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	_, err := w.ResponseWriter.Write(b)
	if err != nil {
		return 0, err
	}
	return length, nil
}
