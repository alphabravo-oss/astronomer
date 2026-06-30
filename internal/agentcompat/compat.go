package agentcompat

import (
	"strconv"
	"strings"
)

const (
	// 0.x release line: v0.2.0 (the current release) and newer are fully
	// supported; v0.1.x is deprecated (still connects, but warns to upgrade);
	// anything older than v0.1.0 is blocked from connecting. Bump the supported
	// floor as the release line advances; at the 1.0 cutover set it to v1.0.0.
	// Keep MinimumSupportedVersion's numeric value in step with the current
	// release in pkg/version / deploy/chart.
	MinimumCompatibleVersion = "v0.1.0"
	MinimumSupportedVersion  = "v0.2.0"
	minimumCompatibleMajor   = 0
	minimumCompatibleMinor   = 1
	minimumSupportedMajor    = 0
	minimumSupportedMinor    = 2
)

type Status struct {
	Status         string
	Message        string
	DegradedReason string
	Blocked        bool
}

func Evaluate(version string) Status {
	major, minor, ok := parseMajorMinor(version)
	if !ok {
		return Status{
			Status:  "unknown",
			Message: "Agent version is not reported in a parseable semver format.",
		}
	}
	if major < minimumCompatibleMajor || (major == minimumCompatibleMajor && minor < minimumCompatibleMinor) {
		return Status{
			Status:         "blocked",
			Message:        "Agent is below the minimum compatible version " + MinimumCompatibleVersion + ".",
			DegradedReason: "agent version is blocked; upgrade before reconnecting",
			Blocked:        true,
		}
	}
	if major < minimumSupportedMajor || (major == minimumSupportedMajor && minor < minimumSupportedMinor) {
		return Status{
			Status:         "deprecated",
			Message:        "Agent is below the minimum supported version " + MinimumSupportedVersion + ".",
			DegradedReason: "agent version is deprecated; plan an upgrade",
		}
	}
	return Status{
		Status:  "supported",
		Message: "Agent is on the supported compatibility track.",
	}
}

func parseMajorMinor(version string) (int, int, bool) {
	v := strings.TrimSpace(version)
	v = strings.TrimPrefix(v, "v")
	if v == "" || v == "latest" {
		return 0, 0, false
	}
	parts := strings.Split(v, ".")
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return 0, 0, false
	}
	minor := 0
	if len(parts) > 1 {
		minor, err = strconv.Atoi(parts[1])
		if err != nil || minor < 0 {
			return 0, 0, false
		}
	}
	return major, minor, true
}
