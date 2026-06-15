// Package proxyhdr centralizes the request-header filtering policy used when
// the tunnel server and the cluster-side agent forward a browser/dashboard
// originated request up to the Kubernetes API (or an in-cluster Service).
//
// The policy is an ALLOWLIST: only headers the proxy explicitly intends to
// forward survive; everything else is dropped. An allowlist fails closed —
// any newly introduced or attacker-supplied header (e.g. the front-proxy
// identity headers X-Remote-User / X-Remote-Group / X-Remote-Extra-* honored
// by clusters running with --requestheader auth, or Kubernetes
// Impersonate-* headers, or the caller's Authorization JWT) is stripped by
// default rather than relying on someone remembering to extend a denylist.
//
// Matching is case-insensitive.
package proxyhdr

import "strings"

// forwardableHeaders is the set of request headers the proxy is willing to
// pass through to the upstream. Keys are lowercase for case-insensitive
// matching. Keep this list minimal: only headers that are genuinely needed
// for the Kubernetes API / Service request to be understood belong here.
//
// Deliberately excluded (must NOT be forwarded; see package doc):
//   - authorization        caller's Astronomer JWT, not a k8s bearer
//   - cookie / host         browser/proxy headers, wrong or noise upstream
//   - x-forwarded-*         proxy hop headers
//   - impersonate-*         user-controlled k8s impersonation
//   - x-remote-*            front-proxy identity (--requestheader auth spoofing)
var forwardableHeaders = map[string]bool{
	"accept":          true,
	"accept-encoding": true,
	"content-type":    true,
	"content-length":  true,
	"user-agent":      true,
}

// ShouldForwardRequestHeader reports whether a request header named name,
// originating from the dashboard/browser, may be forwarded to the upstream.
// It returns false for any header not on the explicit allowlist.
func ShouldForwardRequestHeader(name string) bool {
	return forwardableHeaders[strings.ToLower(strings.TrimSpace(name))]
}
