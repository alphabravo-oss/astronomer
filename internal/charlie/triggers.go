package charlie

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alphabravocompany/astronomer-go/internal/redaction"
)

const (
	DefaultDisconnectGrace = 5 * time.Minute
	DefaultFlapWindow      = 15 * time.Minute
	DefaultFlapCount       = 3
	DefaultTriggerCooldown = 30 * time.Minute
	maxTriggerSummaryBytes = 512
)

type DefaultTriggerRule struct {
	Name             string
	Category         string
	MinimumSeverity  string
	Window           time.Duration
	Cooldown         time.Duration
	Threshold        int
	ModeCeiling      Mode
	EnabledByDefault bool
}

// DefaultTriggerRules is product-owned policy. Charlie receives only events
// that Astronomer has evaluated; the generic Charlie service does not hardcode
// these thresholds and no default rule authorizes a write.
func DefaultTriggerRules() []DefaultTriggerRule {
	readOnly := func(name, category, severity string, window time.Duration, threshold int) DefaultTriggerRule {
		return DefaultTriggerRule{Name: name, Category: category, MinimumSeverity: severity, Window: window, Cooldown: DefaultTriggerCooldown, Threshold: threshold, ModeCeiling: ModeReadOnly, EnabledByDefault: false}
	}
	return []DefaultTriggerRule{
		readOnly("alert_high_critical", "alerts", "warning", time.Minute, 1),
		readOnly("agent_disconnected", "cluster_agents", "warning", DefaultDisconnectGrace, 1),
		readOnly("agent_flapping", "cluster_agents", "warning", DefaultFlapWindow, DefaultFlapCount),
		readOnly("agent_heartbeat_stale", "cluster_agents", "warning", DefaultDisconnectGrace, 1),
		readOnly("agent_auth_registration_failure", "cluster_agents", "critical", 15*time.Minute, 3),
		readOnly("agent_credential_invalid", "cluster_agents", "critical", 15*time.Minute, 1),
		readOnly("agent_downstream_api_unreachable_reported", "cluster_agents", "warning", 10*time.Minute, 1),
		readOnly("agent_version_unsupported", "cluster_agents", "warning", time.Hour, 1),
		readOnly("agent_upgrade_failed_or_stalled", "cluster_agents", "warning", 30*time.Minute, 1),
		readOnly("agent_ingestion_failed", "cluster_agents", "warning", 15*time.Minute, 3),
		readOnly("agent_command_expired", "cluster_agents", "warning", 15*time.Minute, 1),
		readOnly("tunnel_replica_concentration", "tunnel", "warning", 15*time.Minute, 1),
		readOnly("tunnel_locator_failure", "tunnel", "critical", 5*time.Minute, 1),
		readOnly("server_rollout_reconnect_spike", "tunnel", "warning", 15*time.Minute, 1),
		readOnly("cluster_agents_simultaneous_disconnect", "cluster_agents", "critical", 5*time.Minute, 1),
		readOnly("backup_terminal_failure", "workflow", "critical", time.Minute, 1),
		readOnly("restore_drill_terminal_failure", "workflow", "critical", time.Minute, 1),
		readOnly("logging_terminal_failure", "workflow", "critical", time.Minute, 1),
		readOnly("self_management_gitops_failure", "workflow", "critical", time.Minute, 1),
		readOnly("migration_terminal_failure", "workflow", "critical", time.Minute, 1),
		readOnly("queue_terminal_failure", "workflow", "critical", time.Minute, 1),
		readOnly("tunnel_workflow_terminal_failure", "workflow", "critical", time.Minute, 1),
	}
}

type TriggerSignal struct {
	Source         string    `json:"source"`
	EventType      string    `json:"event_type"`
	ResourceType   string    `json:"resource_type"`
	ResourceID     string    `json:"resource_id"`
	FailureClass   string    `json:"failure_class"`
	Severity       string    `json:"severity"`
	State          string    `json:"state"`
	Timestamp      time.Time `json:"timestamp"`
	ProductVersion string    `json:"product_version,omitempty"`
	Environment    string    `json:"environment,omitempty"`
	Region         string    `json:"region,omitempty"`
	Summary        string    `json:"summary"`
}

type PreparedTrigger struct {
	Fingerprint string        `json:"fingerprint"`
	Signal      TriggerSignal `json:"signal"`
}

func PrepareTrigger(signal TriggerSignal) (PreparedTrigger, error) {
	for name, value := range map[string]string{
		"source": signal.Source, "event_type": signal.EventType,
		"resource_type": signal.ResourceType, "resource_id": signal.ResourceID,
		"failure_class": signal.FailureClass, "severity": signal.Severity, "state": signal.State,
	} {
		if strings.TrimSpace(value) == "" || len(value) > 255 {
			return PreparedTrigger{}, fmt.Errorf("Charlie trigger %s is invalid", name)
		}
	}
	if signal.Timestamp.IsZero() {
		return PreparedTrigger{}, fmt.Errorf("Charlie trigger timestamp is required")
	}
	signal.Summary = truncateUTF8(redaction.SensitiveLine(strings.TrimSpace(signal.Summary)), maxTriggerSummaryBytes)
	fingerprint := stableFingerprint(signal.Source, signal.ResourceType, signal.ResourceID, signal.FailureClass, signal.State)
	return PreparedTrigger{Fingerprint: fingerprint, Signal: signal}, nil
}

func stableFingerprint(values ...string) string {
	for index := range values {
		values[index] = strings.ToLower(strings.TrimSpace(values[index]))
	}
	digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(digest[:])
}

// FindingDedupeFingerprint deliberately excludes prose, timestamps, and mode.
// The same diagnosis/action for the same affected product resource is one
// lifecycle that can coalesce, expire, and reopen.
func FindingDedupeFingerprint(installationID, resourceType, resourceID, normalizedDiagnosis, recommendedCapability string) string {
	return stableFingerprint(installationID, resourceType, resourceID, normalizedDiagnosis, recommendedCapability)
}

type ConnectionObservation struct {
	State      string
	OccurredAt time.Time
}

func DisconnectedBeyondGrace(observations []ConnectionObservation, now time.Time, grace time.Duration) bool {
	if grace <= 0 {
		grace = DefaultDisconnectGrace
	}
	if len(observations) == 0 {
		return false
	}
	sorted := append([]ConnectionObservation(nil), observations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].OccurredAt.Before(sorted[j].OccurredAt) })
	last := sorted[len(sorted)-1]
	return last.State == "disconnected" && !last.OccurredAt.Add(grace).After(now)
}

func FlappingInsideWindow(observations []ConnectionObservation, now time.Time, window time.Duration, threshold int) bool {
	if window <= 0 {
		window = DefaultFlapWindow
	}
	if threshold <= 0 {
		threshold = DefaultFlapCount
	}
	cutoff := now.Add(-window)
	disconnects := 0
	for _, observation := range observations {
		if observation.State == "disconnected" && !observation.OccurredAt.Before(cutoff) && !observation.OccurredAt.After(now) {
			disconnects++
		}
	}
	return disconnects >= threshold
}

func truncateUTF8(value string, maximumBytes int) string {
	if len(value) <= maximumBytes {
		return value
	}
	value = value[:maximumBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
