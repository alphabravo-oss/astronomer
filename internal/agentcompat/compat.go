package agentcompat

import (
	"fmt"
	"sort"
	"strings"

	semver "github.com/Masterminds/semver/v3"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	MinimumCompatibleVersion = protocol.MinimumCompatibleAgentVersion
	MinimumSupportedVersion  = protocol.MinimumSupportedAgentVersion
)

var (
	minimumCompatible = semver.MustParse(protocol.MinimumCompatibleAgentVersion)
	minimumSupported  = semver.MustParse(protocol.MinimumSupportedAgentVersion)
	maximumExclusive  = semver.MustParse(protocol.MaximumSupportedAgentVersionExclusive)
)

type Status struct {
	Status                string
	Code                  string
	Message               string
	DegradedReason        string
	UpgradeRecommendation string
	Blocked               bool
}

// Evaluate classifies the independently versioned agent application against
// the generated release compatibility contract.
func Evaluate(version string) Status {
	parsed, err := strictVersion(version)
	if err != nil {
		return blocked("unknown", "agent_version_invalid",
			"Agent version is missing or is not strict semantic versioning.",
			"Install a signed agent from the supported release line.")
	}
	if parsed.LessThan(minimumCompatible) {
		return blocked("blocked", "agent_version_too_old",
			"Agent is below the minimum compatible version "+MinimumCompatibleVersion+".",
			"Upgrade the agent to "+MinimumSupportedVersion+" or newer before reconnecting.")
	}
	if !parsed.LessThan(maximumExclusive) {
		return blocked("blocked", "agent_version_future_major",
			"Agent is newer than this server's supported application range.",
			"Upgrade the Astronomer management plane before reconnecting this agent.")
	}
	if parsed.LessThan(minimumSupported) {
		return Status{
			Status:                "deprecated",
			Code:                  "agent_version_deprecated",
			Message:               "Agent is below the minimum supported version " + MinimumSupportedVersion + ".",
			DegradedReason:        "agent version is deprecated; plan an upgrade",
			UpgradeRecommendation: "Upgrade the agent to " + MinimumSupportedVersion + " or newer during this release window.",
		}
	}
	return Status{Status: "supported", Code: "compatible", Message: "Agent is on the supported compatibility track."}
}

// EvaluateConnect validates application, tunnel, heartbeat, delivery, and
// capability versions independently. Authentication and work dispatch happen
// only after this admission succeeds.
func EvaluateConnect(payload protocol.ConnectPayload) Status {
	if status := Evaluate(payload.AgentVersion); status.Blocked {
		return status
	}
	if payload.TunnelProtocolVersion < protocol.MinimumTunnelProtocolVersion || payload.TunnelProtocolVersion > protocol.MaximumTunnelProtocolVersion {
		return protocolBlocked("tunnel_protocol_unsupported", "tunnel protocol", payload.TunnelProtocolVersion,
			protocol.MinimumTunnelProtocolVersion, protocol.MaximumTunnelProtocolVersion)
	}
	if payload.HeartbeatSchemaVersion < protocol.MinimumHeartbeatSchemaVersion || payload.HeartbeatSchemaVersion > protocol.MaximumHeartbeatSchemaVersion {
		return protocolBlocked("heartbeat_schema_unsupported", "heartbeat schema", payload.HeartbeatSchemaVersion,
			protocol.MinimumHeartbeatSchemaVersion, protocol.MaximumHeartbeatSchemaVersion)
	}
	if payload.DeliveryProtocolVersion != protocol.DeliveryProtocolVersion {
		return blocked("blocked", "delivery_protocol_unsupported",
			fmt.Sprintf("Delivery protocol %q is not supported; expected %q.", payload.DeliveryProtocolVersion, protocol.DeliveryProtocolVersion),
			"Install an agent from a release compatible with "+protocol.SupportedConnectContract()+".")
	}
	missing, malformed := missingCapabilities(payload.Capabilities, protocol.RequiredConnectCapabilities())
	if malformed {
		return blocked("blocked", "capability_advertisement_invalid",
			"Agent capability advertisement is empty, duplicated, or exceeds protocol limits.",
			"Install a supported agent and retry enrollment.")
	}
	if len(missing) > 0 {
		return blocked("blocked", "required_capability_missing",
			"Agent is missing required capabilities: "+strings.Join(missing, ", ")+".",
			"Upgrade or reconfigure the agent before reconnecting.")
	}
	return Evaluate(payload.AgentVersion)
}

func strictVersion(value string) (*semver.Version, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	if value == "" || strings.Count(value, ".") != 2 {
		return nil, fmt.Errorf("strict semantic version required")
	}
	return semver.StrictNewVersion(value)
}

func protocolBlocked(code, label string, got, minimum, maximum int) Status {
	return blocked("blocked", code,
		fmt.Sprintf("Agent %s version %d is outside the supported range %d-%d.", label, got, minimum, maximum),
		"Install an agent from a release compatible with "+protocol.SupportedConnectContract()+".")
}

func blocked(status, code, message, recommendation string) Status {
	return Status{
		Status: status, Code: code, Message: message, DegradedReason: code,
		UpgradeRecommendation: recommendation, Blocked: true,
	}
}

func missingCapabilities(advertised, required []string) ([]string, bool) {
	if len(advertised) == 0 || len(advertised) > 128 {
		return nil, true
	}
	set := make(map[string]struct{}, len(advertised))
	for _, capability := range advertised {
		capability = strings.TrimSpace(capability)
		if capability == "" || len(capability) > 128 {
			return nil, true
		}
		if _, duplicate := set[capability]; duplicate {
			return nil, true
		}
		set[capability] = struct{}{}
	}
	missing := make([]string, 0)
	for _, capability := range required {
		if _, found := set[capability]; !found {
			missing = append(missing, capability)
		}
	}
	sort.Strings(missing)
	return missing, false
}
