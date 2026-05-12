package webhook

import "strings"

// MatchFilters reports whether eventName matches any of the supplied
// fnmatch-style globs.
//
// Glob syntax (deliberately small — most operators expect path.Match
// semantics from prior tooling):
//
//	*  — matches zero or more characters of any kind
//	?  — matches exactly one character
//	,  — alternative separator inside a single glob string
//
// Examples:
//
//	"audit.*"             matches "audit.user.login", "audit.cluster.create"
//	"cluster.*,user.*"    matches anything starting with "cluster." OR "user."
//	"auth.login_failed"   exact match
//	""                    matches everything (empty filter = no filter)
//
// An empty glob list matches every event — operators registering a
// subscription without an explicit filter expect to receive everything.
// The handler also enforces "supply at least one glob" to make this
// behaviour opt-in, but the matcher itself stays permissive so a
// migration of a hand-edited row that ends up with an empty list still
// behaves predictably.
func MatchFilters(eventName string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, raw := range filters {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			// An empty filter inside an otherwise-non-empty list is a
			// match-all wildcard — same semantics as the empty-list
			// case above.
			return true
		}
		// Comma allows multiple alternatives in one entry. We split
		// before trimming so "audit.*, cluster.*" with the space after
		// the comma works.
		for _, alt := range strings.Split(raw, ",") {
			alt = strings.TrimSpace(alt)
			if alt == "" {
				continue
			}
			if globMatch(alt, eventName) {
				return true
			}
		}
	}
	return false
}

// globMatch is a small fnmatch-style implementation: '*' matches any
// run of characters, '?' matches exactly one. We don't support character
// classes ([abc]) because the event-name surface is `[a-z0-9._]+` so
// they'd add complexity without buying anything.
func globMatch(pattern, name string) bool {
	// Fast-path: no wildcards — exact match.
	if !strings.ContainsAny(pattern, "*?") {
		return pattern == name
	}
	return matchGreedy(pattern, name)
}

// matchGreedy is a recursive-descent matcher with O(len(name)) backtracking
// on '*'. For the realistic input shape (event names < 64 chars, patterns
// < 32 chars) this is fine; we'd reach for a state-machine implementation
// if patterns grew teeth.
func matchGreedy(pattern, s string) bool {
	for {
		if len(pattern) == 0 {
			return len(s) == 0
		}
		switch pattern[0] {
		case '*':
			// Trim consecutive stars — they're equivalent to a single
			// star.
			i := 0
			for i < len(pattern) && pattern[i] == '*' {
				i++
			}
			rest := pattern[i:]
			if len(rest) == 0 {
				// Trailing '*' matches anything remaining.
				return true
			}
			for j := 0; j <= len(s); j++ {
				if matchGreedy(rest, s[j:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			pattern = pattern[1:]
			s = s[1:]
		default:
			if len(s) == 0 || pattern[0] != s[0] {
				return false
			}
			pattern = pattern[1:]
			s = s[1:]
		}
	}
}
