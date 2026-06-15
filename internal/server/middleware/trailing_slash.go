// Trailing-slash normalisation.
//
// Most of our REST routes are mounted with a trailing slash (e.g.
// /api/v1/clusters/{id}/), but a chunk of the frontend api client
// historically called those routes WITHOUT the trailing slash. chi's
// router does exact-match URL routing, so `DELETE /clusters/{id}`
// 404s when `/clusters/{id}/` is what's registered.
//
// The user-facing symptom of this is exactly what was reported as
// "deleting clusters in the UI doesn't work — it shows decommissioned
// or doesn't go away at all": the frontend mutation receives a 404,
// the react-query cache invalidation never fires, the cluster row
// stays on the list. Most of our 20+ DELETE / PUT call sites have
// the same shape, so patching the frontend one-by-one would be a
// 50-file PR.
//
// Fix at the server layer: a tiny middleware that rewrites the
// request's URL.Path to end with "/" before chi sees it, scoped to
// the API namespace so it doesn't affect helm-repo (those routes
// don't expect trailing slashes). The redirect-then-retry approach
// chi.RedirectSlashes uses is technically correct but means an
// extra round-trip and can drop request methods on older clients;
// rewriting in-process is single-hop and method-safe.

package middleware

import (
	"net/http"
	"strings"
)

// NormalizeAPITrailingSlash returns a middleware that adds a trailing
// slash to /api/v1/* requests that don't already have one. Other
// paths (helm-repo, /argocd, static assets) pass through untouched.
//
// Exclusions:
//   - WebSocket upgrade paths under /api/v1/ws/* keep their original
//     URL because the WS handlers use chi.URLParam against the path
//     including the trailing component (cluster_id, session_id).
//     Adding a trailing slash to `…/tunnel/{cluster_id}` would
//     produce `…/tunnel/{cluster_id}/` which chi doesn't have a
//     route for either way — leave them alone.
//   - Paths ending in a file extension (e.g. /openapi.yaml) are
//     left alone because static-asset routes don't tolerate the
//     extra slash.
func NormalizeAPITrailingSlash(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if shouldAddTrailingSlash(p) {
			r2 := r.Clone(r.Context())
			r2.URL.Path = p + "/"
			r2.RequestURI = r2.URL.RequestURI()
			next.ServeHTTP(w, r2)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// shouldAddTrailingSlash is the inclusion gate: only /api/v1/* paths
// that don't already end with "/" and aren't WS / file-extension
// shaped. Pulled out so the unit test can table-drive the matrix
// without spinning up a full chi router.
func shouldAddTrailingSlash(p string) bool {
	if !strings.HasPrefix(p, "/api/v1/") {
		return false
	}
	if strings.HasSuffix(p, "/") {
		return false
	}
	if strings.HasPrefix(p, "/api/v1/ws/") {
		return false
	}
	// k8s proxy passthrough: everything after /k8s/ is a verbatim Kubernetes
	// API path forwarded to the agent/apiserver as-is. The proxy route is a
	// chi wildcard (…/k8s/*) that matches with or without a trailing slash, so
	// appending one here corrupts the upstream path — e.g. /openapi/v2 ->
	// /openapi/v2/, which the apiserver 404s (breaking ArgoCD's cluster-cache
	// OpenAPI fetch and thus baseline provisioning).
	if strings.Contains(p, "/k8s/") {
		return false
	}
	// Last segment looks like a file (foo.bar) → leave alone. Cheap
	// heuristic: '.' after the final '/'. We don't care about
	// false-positives from dotted resource names (rare in REST URLs)
	// because the worst case is no slash is added and the route 404s
	// the same way it did before this middleware existed.
	last := p
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		last = p[idx+1:]
	}
	if strings.Contains(last, ".") {
		return false
	}
	return true
}
