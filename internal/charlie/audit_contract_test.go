package charlie

import (
	"strings"
	"testing"
)

func validAuditFields(prefix, resourceType string) map[string]any {
	digest := strings.Repeat("a", 64)
	switch prefix {
	case "charlie.http.":
		return map[string]any{"outcome_code": "success", "method": "GET", "status_code": 200, "duration_ms": int64(2)}
	case "admin.charlie.agent.", "admin.charlie.disconnect", "admin.charlie.disclosure.", "admin.charlie.action_policy.":
		return map[string]any{"outcome_code": "completed"}
	case "admin.charlie.mode.":
		return map[string]any{"outcome_code": "completed", "mode": "read_only", "revision": int64(2)}
	case "admin.charlie.trigger.":
		if resourceType == "charlie_trigger_rule" {
			return map[string]any{"outcome_code": "completed", "enabled": true, "suppressed": false}
		}
		return map[string]any{"outcome_code": "completed"}
	case "admin.charlie.access.":
		return map[string]any{"outcome_code": "completed", "enabled": true}
	case "admin.charlie.diagnostics.":
		return map[string]any{"outcome_code": "completed", "overall": "healthy"}
	case "charlie.connection.", "charlie.certificate.", "charlie.agent.":
		return map[string]any{"outcome_code": "completed"}
	case "charlie.mode.":
		return map[string]any{"outcome_code": "completed", "mode": "read_only", "revision": int64(2)}
	case "charlie.trigger.":
		return map[string]any{"outcome_code": "completed", "attempt": int32(1)}
	case "charlie.mcp.":
		return map[string]any{"outcome_code": "authority.read_only_write", "capability_digest": digest, "effect": "write", "denial_code": "authority.read_only_write", "mode_revision": int64(1), "policy_revision": int64(2), "fencing_epoch": int64(3)}
	case "charlie.session.":
		return map[string]any{"outcome_code": "allowed", "visibility": "private", "resource_count": 1}
	case "charlie.finding.":
		return map[string]any{"outcome_code": "replayed", "resource_count": 1}
	case "charlie.approval.":
		return map[string]any{"outcome_code": "completed", "approval_digest": digest, "action_digest": digest, "manifest_digest": "sha256:" + digest, "capability": "astronomer.queue.retry_task", "decision": "approve"}
	case "charlie.action.reconciled_":
		return map[string]any{"outcome": "succeeded", "attempt": int32(1), "fencing_epoch": int64(2), "action_digest": digest, "capability": "astronomer.queue.retry_task"}
	default:
		return map[string]any{"phase": "denied", "action_digest": digest, "argument_digest": digest, "authorization_digest": digest, "capability_digest": digest, "effect": "write", "state": "denied", "denial_code": "authority.read_only_write", "result_digest": "", "mode_revision": int64(1), "policy_revision": int64(2), "fencing_epoch": int64(3)}
	}
}

func TestCharlieAuditContractIsMachineReadableAndCoversApplicableOutcomes(t *testing.T) {
	contract, err := CharlieAuditContract()
	if err != nil {
		t.Fatal(err)
	}
	if contract.Version != 1 || len(contract.Events) < 5 || len(contract.ForbiddenFieldFragments) < 10 {
		t.Fatalf("incomplete Charlie audit contract: %#v", contract)
	}
	for _, event := range contract.Events {
		classes := map[string]bool{}
		for _, class := range event.Coverage {
			classes[class] = true
		}
		if !classes["redaction"] || !classes["success"] && !classes["denial"] {
			t.Fatalf("event %q lacks applicable outcome/redaction coverage: %v", event.Prefix, event.Coverage)
		}
		action := event.Actions[0]
		if _, err := EncodeCharlieAuditDetail(action, event.ResourceType, validAuditFields(event.Prefix, event.ResourceType)); err != nil {
			t.Fatalf("valid event %q rejected: %v", action, err)
		}
	}
}

func TestCharlieCanonicalLifecycleVocabularyIsRepresentedInContract(t *testing.T) {
	resources := map[string]string{
		"connection": AuditResourceConnection, "certificate": AuditResourceCertificate,
		"agent": AuditResourceAgent, "mode": AuditResourceMode, "session": AuditResourceSession,
		"trigger": AuditResourceTrigger, "approval": AuditResourceApproval,
		"mcp": AuditResourceMCPDecision, "finding": AuditResourceFinding,
	}
	contract, err := CharlieAuditContract()
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range AuditActions {
		parts := strings.Split(action, ".")
		resourceType := resources[parts[1]]
		if _, ok := matchAuditEvent(contract.Events, action, resourceType); !ok {
			t.Fatalf("canonical lifecycle action %q (%s) is absent from the machine-readable contract", action, resourceType)
		}
	}
}

func TestCharlieAuditEncoderRejectsUnknownAndForbiddenContentForEverySink(t *testing.T) {
	contract, err := CharlieAuditContract()
	if err != nil {
		t.Fatal(err)
	}
	const sentinel = "prompt-token-certificate-provider-error-SENTINEL"
	for _, event := range contract.Events {
		action := event.Actions[0]
		fields := validAuditFields(event.Prefix, event.ResourceType)
		fields["prompt"] = sentinel
		if encoded, err := EncodeCharlieAuditDetail(action, event.ResourceType, fields); err == nil || strings.Contains(string(encoded), sentinel) {
			t.Fatalf("event %q admitted forbidden content: %s", action, encoded)
		}
	}
	if _, err := EncodeCharlieAuditDetail("charlie.unknown.event", "charlie_unknown", map[string]any{"outcome_code": "allowed"}); err == nil {
		t.Fatal("unknown Charlie audit event was admitted")
	}
	if _, err := EncodeCharlieAuditDetail("charlie.session.attacker_chosen", "charlie_session", map[string]any{"outcome_code": "allowed", "visibility": "private", "resource_count": 0}); err == nil {
		t.Fatal("unknown action sharing an allowlisted prefix was admitted")
	}
}

func TestCharlieAuditEncoderRejectsInvalidEnumsDigestsCountsAndArbitraryStrings(t *testing.T) {
	tests := []struct {
		name, action, resource string
		fields                 map[string]any
	}{
		{"visibility", "charlie.session.read", "charlie_session", map[string]any{"visibility": "private-SENTINEL", "outcome_code": "allowed", "resource_count": 1}},
		{"digest", "charlie.approval.approved", "charlie_approval", map[string]any{"approval_digest": "secret", "action_digest": strings.Repeat("a", 64), "manifest_digest": strings.Repeat("a", 64), "capability": "safe.capability", "decision": "approve", "outcome_code": "completed"}},
		{"count", "charlie.finding.read", "charlie_finding", map[string]any{"outcome_code": "allowed", "resource_count": -1}},
		{"code", "charlie.finding.read", "charlie_finding", map[string]any{"outcome_code": "raw provider error with spaces", "resource_count": 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := EncodeCharlieAuditDetail(test.action, test.resource, test.fields); err == nil {
				t.Fatal("invalid Charlie audit detail was admitted")
			}
		})
	}
}
