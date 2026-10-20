package version

import "strings"

// Set via ldflags at build time from the git tag (the release workflow stamps
// VERSION/GIT_COMMIT/BUILD_DATE into every binary). This default is the single
// source of truth for UN-released dev builds: bump it when the dev cycle moves
// to the next version. Keep it in sync with deploy/chart/Chart.yaml appVersion
// (which carries no "v"; Version always does, see Normalize).
//
// Version is normalized once at package init, so whatever the ldflags stamp
// ("2.0.1", "v2.0.1", "dev-<sha>") every reader sees the canonical form.
var (
	Version   = "v2.0.1"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// Normalize returns the canonical spelling of a build version. A SemVer-shaped
// value always carries a lower-case "v" ("2.0.1" and "V2.0.1" become
// "v2.0.1"); anything else, such as a "dev-<sha>" local build, is returned
// trimmed and otherwise unchanged. Every place that stamps or compares an
// agent/server version (system release agent_version, the agent's reported
// inventory, readiness) goes through this function, so "2.0.1" and "v2.0.1"
// cannot disagree.
func Normalize(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return v
	}
	switch {
	case v[0] >= '0' && v[0] <= '9':
		return "v" + v
	case v[0] == 'V' && len(v) > 1 && v[1] >= '0' && v[1] <= '9':
		return "v" + v[1:]
	default:
		return v
	}
}
