// Package egress emits the startup "egress endpoints" log line every service
// writes before it contacts anything: the static out-of-cluster endpoints it
// will reach and the categories of endpoints that are configured at runtime.
// The line is greppable with `msg="egress endpoints"` / "egress endpoints".
package egress

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strings"
)

// Set collects endpoints for one service.
type Set struct {
	static  []string
	dynamic []string
}

// Add records endpoint (a URL, host[:port] or registry base) when it is
// outside the cluster. In-cluster service names, loopback and empty values
// are ignored.
func (s *Set) Add(endpoint string) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" || !OutOfCluster(endpoint) {
		return
	}
	if !slices.Contains(s.static, endpoint) {
		s.static = append(s.static, endpoint)
	}
}

// Dynamic records a category of endpoints only known at runtime (for
// example "smtp:platform_settings" or "webhooks:database").
func (s *Set) Dynamic(category string) {
	if !slices.Contains(s.dynamic, category) {
		s.dynamic = append(s.dynamic, category)
	}
}

// Log writes the single INFO line.
func (s *Set) Log(ctx context.Context, logger *slog.Logger, service string) {
	if logger == nil {
		logger = slog.Default()
	}
	static := slices.Clone(s.static)
	slices.Sort(static)
	dynamic := slices.Clone(s.dynamic)
	slices.Sort(dynamic)
	if static == nil {
		static = []string{}
	}
	if dynamic == nil {
		dynamic = []string{}
	}
	logger.InfoContext(ctx, "egress endpoints", "service", service, "endpoints", static, "dynamic", dynamic)
}

// Host extracts the host from a URL, oci:// reference or host[:port][/path].
func Host(endpoint string) string {
	endpoint = strings.TrimPrefix(endpoint, "oci://")
	if strings.Contains(endpoint, "://") {
		if parsed, err := url.Parse(endpoint); err == nil {
			return strings.ToLower(parsed.Hostname())
		}
	}
	host, _, _ := strings.Cut(endpoint, "/")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.Trim(host, "[]"))
}

// OutOfCluster reports whether endpoint leaves the cluster: anything that is
// not a Kubernetes service name (single label, *.svc, *.cluster.local),
// loopback, or a link-local/cluster-private literal is treated as egress.
// Private IP literals are reported (they are usually out-of-cluster hosts).
func OutOfCluster(endpoint string) bool {
	host := Host(endpoint)
	switch {
	case host == "", host == "localhost":
		return false
	case !strings.Contains(host, "."):
		if ip := net.ParseIP(host); ip != nil {
			return !ip.IsLoopback()
		}
		return false
	case strings.HasSuffix(host, ".svc"), strings.Contains(host, ".svc."), strings.HasSuffix(host, ".cluster.local"):
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return false
	}
	return true
}
