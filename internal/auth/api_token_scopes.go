// Package auth — API-token scope vocabulary + IP allowlist matcher.
//
// The runtime enforces scopes on API-token-authenticated requests only:
// JWT (dashboard) sessions are session-scoped by the RBAC engine and
// don't carry a per-token scope claim. The vocabulary here is the
// coarse-grained set that covers our compliance surface (PCI-DSS,
// SOC 2) — operators wanting finer control can still pin a token to
// the empty-scope-list legacy behaviour ("scopes": []), which is
// treated as "no API-token-level enforcement" so the rollout is
// strictly opt-in per token.
package auth

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

// API token scope vocabulary. The CRUD UI surfaces these as a checkbox
// group; the validator below treats unknown strings as opaque (we don't
// enforce a closed set today so platform operators can mint custom
// names — the closed set lives in the UI / docs).
const (
	// ScopeReadOnly: any GET request inside /api/v1/. Lower bound for
	// every monitoring / dashboard automation we ship.
	ScopeReadOnly = "read"
	// ScopeWriteClusters: cluster CRUD + registration + decommission.
	ScopeWriteClusters = "clusters:write"
	// ScopeWriteProjects: project CRUD + namespace add/remove + policy.
	ScopeWriteProjects = "projects:write"
	// ScopeWriteRBAC: roles / role-bindings / group mappings.
	ScopeWriteRBAC = "rbac:write"
	// ScopeAdmin: superset that grants everything. Tokens carrying
	// `admin` are noisy on purpose — every successful auth audits
	// `actor_auth_method=api_token` and emits the elevated scope so
	// reviewers can spot operational vs. compliance mismatches.
	ScopeAdmin = "admin"
	// ScopeWildcard: the literal `*` is accepted as a synonym for
	// ScopeAdmin so older clients minted by ops tooling keep working.
	ScopeWildcard = "*"
)

// AllowedScopes is the canonical set of values surfaced in the UI.
// Validator-side this list is informational — the matcher in
// ScopeAllowsRequest just does string equality.
var AllowedScopes = []string{
	ScopeReadOnly,
	ScopeWriteClusters,
	ScopeWriteProjects,
	ScopeWriteRBAC,
	ScopeAdmin,
}

// ParseTokenScopes decodes the api_tokens.scopes JSONB column into a
// Go slice. Tolerant of nil / `null` / `[]` (all treated as "no
// scope-level enforcement"). Bad JSON is reported up so the validator
// can fail-closed rather than silently allowing the request.
func ParseTokenScopes(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var scopes []string
	if err := json.Unmarshal(raw, &scopes); err != nil {
		return nil, err
	}
	out := scopes[:0]
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// ScopeAllowsRequest decides whether the supplied token scopes are
// sufficient to satisfy `required`.
//
//   - Empty `scopes` slice => legacy token, NO scope-level enforcement
//     (preserves backward compatibility with pre-044 tokens).
//   - ScopeAdmin or ScopeWildcard ("*") => allow everything.
//   - Otherwise => the required scope must be in the slice verbatim.
//
// `required` may be empty when the route doesn't need scope enforcement
// (e.g. /auth/me/), in which case any non-revoked token passes.
func ScopeAllowsRequest(scopes []string, required string) bool {
	if required == "" {
		return true
	}
	if len(scopes) == 0 {
		// Legacy / unset — opt-in semantics: no scopes means the
		// older "all requests allowed" behaviour. Operators who
		// want enforcement set at least one scope on the token.
		return true
	}
	for _, s := range scopes {
		if s == ScopeAdmin || s == ScopeWildcard {
			return true
		}
		if s == required {
			return true
		}
	}
	return false
}

// ScopeForMethod returns the canonical scope required by an HTTP
// method when the route doesn't pin a more specific one. Used by the
// fallthrough middleware on grouped routes — GET maps to ScopeReadOnly
// and any mutating verb is rejected unless the route opted in to a
// specific write scope. Routes that DO opt in pass the explicit value
// to APITokenScopeEnforce.
func ScopeForMethod(method string) string {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return ScopeReadOnly
	default:
		return ""
	}
}

// ParseAllowedCIDRs splits the comma-separated allowed_cidrs column
// into a slice of *net.IPNet. Empty input yields nil — the validator
// treats nil as "no IP restriction" so legacy tokens keep working.
// Whitespace around entries is trimmed; bare-IP entries (no /mask)
// are auto-promoted to /32 for IPv4 and /128 for IPv6 so the CRUD
// UI can accept either form. Returns the first parse error so a
// misconfigured row fails-closed rather than silently allowing
// every IP.
func ParseAllowedCIDRs(raw string) ([]*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]*net.IPNet, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Auto-promote bare IP -> single-host CIDR.
		if !strings.Contains(p, "/") {
			ip := net.ParseIP(p)
			if ip == nil {
				return nil, &cidrParseError{raw: p}
			}
			if ip.To4() != nil {
				p = p + "/32"
			} else {
				p = p + "/128"
			}
		}
		_, ipnet, err := net.ParseCIDR(p)
		if err != nil {
			return nil, err
		}
		out = append(out, ipnet)
	}
	return out, nil
}

// IPAllowed reports whether `remote` (parsed) matches any CIDR in
// `nets`. nil/empty `nets` means "no restriction" — caller is
// responsible for treating that as allow-all.
func IPAllowed(nets []*net.IPNet, remote net.IP) bool {
	if remote == nil {
		// Couldn't parse — fail closed so a misconfigured proxy
		// stripping X-Forwarded-For doesn't accidentally bypass
		// the allowlist.
		return false
	}
	for _, n := range nets {
		if n == nil {
			continue
		}
		if n.Contains(remote) {
			return true
		}
	}
	return false
}

// RemoteIPForRequest returns the parsed client IP from a chi-wired
// request. chimiddleware.RealIP replaces r.RemoteAddr with the
// X-Forwarded-For value when configured, so we honour that first
// and fall back to RemoteAddr's host portion. Returns nil when no
// parseable value is found.
func RemoteIPForRequest(r *http.Request) net.IP {
	if r == nil {
		return nil
	}
	candidates := []string{}
	if r.RemoteAddr != "" {
		// chimiddleware.RealIP overwrites r.RemoteAddr with the
		// XFF / X-Real-IP value in-place when it runs. Strip any
		// port suffix net/http might have appended.
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			candidates = append(candidates, r.RemoteAddr)
		} else {
			candidates = append(candidates, host)
		}
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		// First entry in the conventional XFF chain is the
		// original client. RealIP middleware will have already
		// promoted this into r.RemoteAddr in production, but
		// preserving the explicit-header path keeps the helper
		// useful in tests that don't wire RealIP.
		if idx := strings.Index(xff, ","); idx != -1 {
			candidates = append(candidates, strings.TrimSpace(xff[:idx]))
		} else {
			candidates = append(candidates, xff)
		}
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		candidates = append(candidates, xri)
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		ip := net.ParseIP(c)
		if ip != nil {
			return ip
		}
	}
	return nil
}

// cidrParseError mirrors net.ParseError shape so callers can switch on
// .Error() the same way; kept private because the package doesn't
// need a richer type today.
type cidrParseError struct {
	raw string
}

func (e *cidrParseError) Error() string {
	return "invalid CIDR or IP: " + e.raw
}
