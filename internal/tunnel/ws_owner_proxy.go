// WebSocket cross-pod proxy.
//
// The HTTP path has had cross-pod fallback (proxy.go:forwardToOwnerPod)
// since the redis locator landed: when the in-memory Hub of the
// receiving server pod doesn't own a cluster's tunnel, the request is
// reverse-proxied to whichever sibling pod the locator points at.
//
// The WS exec / shell path historically did NOT have this fallback —
// ExecConsumer.proxyToAgent simply returned "Cluster agent not
// connected" when the local Hub lookup failed. Multi-replica server
// deployments (the .247 stack runs 2 replicas) saw shell sessions
// fail whenever nginx pinned the WS handshake to the wrong pod.
//
// This file plugs that gap. ForwardWSToOwnerPod is invoked by the
// shell handler BEFORE it calls websocket.Accept, so the upgrade
// itself lands on the sibling pod. Go's httputil.ReverseProxy
// transparently handles the HTTP/1.1 Upgrade dance on WebSocket
// requests (added in 1.19), so we don't need to hand-roll the
// hijack-and-pipe loop.

package tunnel

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// ForwardWSToOwnerPod reverse-proxies an HTTP-Upgrade WebSocket
// request to the sibling pod that owns the cluster's tunnel.
// Returns true when the request was handled (the response was
// written by the proxy), false when the caller should fall through
// to its existing local-handling path.
//
// Returns false in three cases:
//
//   - The hub has no locator (single-pod deployment; no sibling).
//   - The locator has no entry (the cluster's tunnel has not been
//     observed recently; the agent really is disconnected).
//   - The locator points at our own address (stale entry from a
//     prior life of this pod; falling through surfaces the real
//     "agent disconnected" error instead of a self-forward loop).
//
// The proxy strips the X-Astronomer-Forwarded-By header on the way
// out and re-stamps it with our address. The sibling pod sees the
// header and refuses to forward again, so we cannot loop.
func ForwardWSToOwnerPod(hub *Hub, log *slog.Logger, w http.ResponseWriter, r *http.Request, clusterID string) bool {
	if hub == nil {
		return false
	}
	loc := hub.Locator()
	if loc == nil {
		return false
	}
	if log == nil {
		log = slog.Default()
	}
	// If this request was already forwarded once, don't bounce it
	// again. The HTTP path uses the same header (proxy.go) — pick a
	// consistent name so an HTTP→WS forward chain can't loop either.
	if r.Header.Get("X-Astronomer-Forwarded-By") != "" {
		return false
	}
	addr, err := loc.Lookup(r.Context(), clusterID)
	if err != nil {
		log.Warn("ws cross-pod: locator lookup failed",
			slog.String("cluster_id", clusterID),
			slog.String("error", err.Error()),
		)
		return false
	}
	if addr == "" {
		return false
	}
	if addr == loc.Address() {
		return false
	}
	// Normalise: locator stores host:port, no scheme. Build a URL so
	// ReverseProxy.Director has something to anchor against.
	target := &url.URL{
		Scheme: "http",
		Host:   addr,
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	// Override Director so we rewrite the URL but preserve the
	// inbound Upgrade headers. The default Director would clobber
	// the Host header but leave Connection/Upgrade alone, which
	// is what we want — but we also stamp our own forwarded marker.
	origDirector := proxy.Director
	log.Info("ws cross-pod: forwarding to sibling",
		slog.String("cluster_id", clusterID),
		slog.String("target", target.Host),
		slog.String("path", r.URL.Path),
	)
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.Header.Set("X-Astronomer-Forwarded-By", loc.Address())
		// Strip hop-by-hop / connection-related headers that nginx
		// or chi middleware may have folded in. The Upgrade /
		// Connection: Upgrade pair must survive — ReverseProxy
		// preserves them for us when the request is an upgrade.
		req.Header.Del("X-Forwarded-Host")
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, perr error) {
		log.Warn("ws cross-pod: proxy error",
			slog.String("cluster_id", clusterID),
			slog.String("target", target.Host),
			slog.String("error", perr.Error()),
		)
		// 503 mirrors what the local path would have returned —
		// the client sees the same error category whether the
		// failure was local or upstream.
		http.Error(rw, fmt.Sprintf(`{"error":"Cluster agent not connected: %s"}`, sanitizeWSProxyError(perr)), http.StatusServiceUnavailable)
	}
	proxy.ServeHTTP(w, r)
	return true
}

// sanitizeWSProxyError trims an error message to a single line that
// doesn't leak internal pod addresses. We surface a coarse cause
// (connection refused / timeout / other) — operators can correlate
// with the pod logs by the X-Astronomer-Forwarded-By header.
func sanitizeWSProxyError(err error) string {
	if err == nil {
		return "unknown"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return "sibling pod refused connection"
	case strings.Contains(s, "i/o timeout"), strings.Contains(s, "deadline exceeded"):
		return "sibling pod timeout"
	default:
		return "sibling pod unreachable"
	}
}
