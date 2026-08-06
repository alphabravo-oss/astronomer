package contract

import (
	"bytes"
	"testing"
	"time"
)

func TestQualificationScenarioContractLoadsCanonicalProfileByCopy(t *testing.T) {
	assertions, timeouts := QualificationScenarioContract()
	if len(assertions) != 27 || len(timeouts) != 27 {
		t.Fatalf("scenario contract sizes = %d/%d, want 27/27", len(assertions), len(timeouts))
	}
	for _, required := range []string{
		"process_absent", "listener_absent", "timer_absent",
		"dns_packets_zero", "tcp_packets_zero", "udp_packets_zero",
		"runtime_counters_unchanged", "downstream_counters_unchanged",
	} {
		if !containsContractValue(assertions["feature_false"], required) {
			t.Fatalf("feature_false omitted %q: %v", required, assertions["feature_false"])
		}
	}
	if !containsContractValue(assertions["central_disabled"], "control_protocol_only") {
		t.Fatalf("central_disabled omitted control_protocol_only: %v", assertions["central_disabled"])
	}
	if timeouts["leader_kill_failover"] != 5*time.Minute || timeouts["upgrade_rollback"] != 20*time.Minute {
		t.Fatalf("unexpected canonical timeouts: leader=%s rollback=%s", timeouts["leader_kill_failover"], timeouts["upgrade_rollback"])
	}

	assertions["feature_false"][0] = "mutated"
	timeouts["feature_false"] = time.Nanosecond
	againAssertions, againTimeouts := QualificationScenarioContract()
	if againAssertions["feature_false"][0] == "mutated" || againTimeouts["feature_false"] == time.Nanosecond {
		t.Fatal("qualification contract returned mutable shared state")
	}
}

func TestQualificationScenarioContractRejectsDrift(t *testing.T) {
	tests := map[string][]byte{
		"unknown field":      bytes.Replace(qualificationProfileJSON, []byte(`"schema":`), []byte(`"unknown":true,"schema":`), 1),
		"duplicate scenario": bytes.Replace(qualificationProfileJSON, []byte(`"id": "unactivated"`), []byte(`"id": "feature_false"`), 1),
		"trailing value":     append(append([]byte(nil), qualificationProfileJSON...), []byte(` {}`)...),
		"missing assertion":  bytes.Replace(qualificationProfileJSON, []byte(`"state_applied"`), []byte(`""`), 1),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeQualificationProfile(raw); err == nil {
				t.Fatal("malformed pinned qualification profile was accepted")
			}
		})
	}
}

func containsContractValue(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
