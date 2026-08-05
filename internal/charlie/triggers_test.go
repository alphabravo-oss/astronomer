package charlie

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultTriggerRulesAreProductOwnedReadOnlyAndOptIn(t *testing.T) {
	rules := DefaultTriggerRules()
	if len(rules) < 20 {
		t.Fatalf("default rule catalog is incomplete: %d", len(rules))
	}
	byName := make(map[string]DefaultTriggerRule, len(rules))
	for _, rule := range rules {
		if _, exists := byName[rule.Name]; exists {
			t.Fatalf("duplicate rule %q", rule.Name)
		}
		byName[rule.Name] = rule
		if rule.ModeCeiling != ModeReadOnly || rule.EnabledByDefault {
			t.Fatalf("default rule silently authorizes runtime work: %+v", rule)
		}
	}
	disconnect := byName["agent_disconnected"]
	if disconnect.Window != 5*time.Minute || disconnect.Threshold != 1 {
		t.Fatalf("disconnect default=%+v", disconnect)
	}
	flapping := byName["agent_flapping"]
	if flapping.Window != 15*time.Minute || flapping.Threshold != 3 {
		t.Fatalf("flapping default=%+v", flapping)
	}
	for _, name := range []string{
		"agent_heartbeat_stale", "agent_auth_registration_failure", "agent_credential_invalid", "agent_downstream_api_unreachable_reported",
		"agent_version_unsupported", "agent_upgrade_failed_or_stalled", "agent_ingestion_failed",
		"agent_command_expired", "tunnel_replica_concentration", "tunnel_locator_failure",
		"server_rollout_reconnect_spike", "fleet_simultaneous_disconnect",
	} {
		if _, ok := byName[name]; !ok {
			t.Errorf("required default rule %q is missing", name)
		}
	}
}

func TestPrepareTriggerFingerprintExcludesMessageAndRedactsSummary(t *testing.T) {
	now := time.Now().UTC()
	base := TriggerSignal{
		Source: "astronomer", EventType: "agent_disconnected", ResourceType: "agent_connection",
		ResourceID: "cluster-a", FailureClass: "heartbeat_stale", Severity: "warning",
		State: "firing", Timestamp: now, Environment: "dev", Region: "us-east",
		Summary: "Authorization: Bearer secret-value",
	}
	first, err := PrepareTrigger(base)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first.Signal.Summary, "secret-value") || len(first.Signal.Summary) > maxTriggerSummaryBytes {
		t.Fatalf("summary not bounded/redacted: %q", first.Signal.Summary)
	}
	base.Summary = "different prose"
	base.Timestamp = now.Add(time.Minute)
	second, err := PrepareTrigger(base)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatal("message text or timestamp changed dedupe fingerprint")
	}
	base.State = "resolved"
	third, _ := PrepareTrigger(base)
	if third.Fingerprint == first.Fingerprint {
		t.Fatal("bounded status did not change fingerprint")
	}
}

func TestDisconnectGraceCancelsOnReconnect(t *testing.T) {
	now := time.Now().UTC()
	observations := []ConnectionObservation{{State: "disconnected", OccurredAt: now.Add(-6 * time.Minute)}}
	if !DisconnectedBeyondGrace(observations, now, 0) {
		t.Fatal("disconnect beyond default grace did not fire")
	}
	observations = append(observations, ConnectionObservation{State: "connected", OccurredAt: now.Add(-time.Minute)})
	if DisconnectedBeyondGrace(observations, now, 0) {
		t.Fatal("reconnect before dispatch did not cancel disconnect trigger")
	}
}

func TestFlappingRequiresThreeDisconnectsInsideFifteenMinutes(t *testing.T) {
	now := time.Now().UTC()
	observations := []ConnectionObservation{
		{State: "disconnected", OccurredAt: now.Add(-14 * time.Minute)},
		{State: "connected", OccurredAt: now.Add(-12 * time.Minute)},
		{State: "disconnected", OccurredAt: now.Add(-9 * time.Minute)},
	}
	if FlappingInsideWindow(observations, now, 0, 0) {
		t.Fatal("two disconnects triggered flapping")
	}
	observations = append(observations, ConnectionObservation{State: "disconnected", OccurredAt: now.Add(-time.Minute)})
	if !FlappingInsideWindow(observations, now, 0, 0) {
		t.Fatal("three disconnects did not trigger flapping")
	}
	observations[0].OccurredAt = now.Add(-16 * time.Minute)
	if FlappingInsideWindow(observations, now, 0, 0) {
		t.Fatal("disconnect outside window contributed to flapping")
	}
}

func TestFindingFingerprintCoalescesRepeatsButSeparatesResourcesAndActions(t *testing.T) {
	base := FindingDedupeFingerprint("install-a", "agent_connection", "cluster-a", "heartbeat_stale", "astronomer.tunnel.refresh_locator_state")
	if base != FindingDedupeFingerprint("INSTALL-A", "agent_connection", "cluster-a", "heartbeat_stale", "astronomer.tunnel.refresh_locator_state") {
		t.Fatal("normalized repeat did not coalesce")
	}
	for _, candidate := range []string{
		FindingDedupeFingerprint("install-a", "agent_connection", "cluster-b", "heartbeat_stale", "astronomer.tunnel.refresh_locator_state"),
		FindingDedupeFingerprint("install-a", "agent_connection", "cluster-a", "heartbeat_stale", "astronomer.tunnel.restart_component"),
	} {
		if candidate == base {
			t.Fatal("distinct resource or recommendation was commingled")
		}
	}
}
