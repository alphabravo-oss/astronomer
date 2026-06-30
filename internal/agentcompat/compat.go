package agentcompat

import (
	"strconv"
	"strings"
)

const (
	// 0.x release line: agents at v0.1.0+ are fully supported; anything older
	// than v0.1.0 is blocked from connecting. The "deprecated" middle tier is
	// dormant during 0.x (minimumSupportedMajor=0 → the major<major check never
	// fires, so nothing is flagged deprecated) and reactivates at the 1.0
	// cutover by bumping minimumSupportedMajor to 1.
	MinimumCompatibleVersion = "v0.1.0"
	MinimumSupportedVersion  = "v0.1.0"
	minimumCompatibleMajor   = 0
	minimumCompatibleMinor   = 1
	minimumSupportedMajor    = 0
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
	if major < minimumSupportedMajor {
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
