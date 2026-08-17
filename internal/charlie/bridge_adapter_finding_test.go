package charlie

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestDecodeBridgeFindingScopeKeepsOnlyExactActionMetadata(t *testing.T) {
	digest := findingResourceDigest("astronomer")
	raw := json.RawMessage(`{"finding":{"finding_id":"finding-a","session_id":"session-a","block_code":"read_only","affected_resources":["sha256:` + digest + `"],"recommended_capability":"astronomer.management.workload_restart","diagnosis":"content-canary","operator_checks":["content-canary"]}}`)
	got, err := bridgeFindingScopeFromEnvelope(decodeFindingEnvelopeForTest(t, raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.FindingID != "finding-a" || got.SessionID != "session-a" || got.ResourceDigest != digest || got.RecommendedCapability != "astronomer.management.workload_restart" {
		t.Fatalf("scope = %#v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "content-canary") {
		t.Fatal("central finding content crossed the background sync boundary")
	}
}

func TestDecodeBridgeFindingScopeRejectsAmbiguousOrSubstitutedTargets(t *testing.T) {
	digest := findingResourceDigest("astronomer")
	for _, raw := range []string{
		`{"finding":{"finding_id":"finding-a","session_id":"session-a","block_code":"read_only","affected_resources":[],"recommended_capability":"astronomer.management.workload_restart"}}`,
		`{"finding":{"finding_id":"finding-a","session_id":"session-a","block_code":"read_only","affected_resources":["sha256:` + digest + `","sha256:` + digest + `"],"recommended_capability":"astronomer.management.workload_restart"}}`,
		`{"finding":{"finding_id":"finding-a","session_id":"session-a","block_code":"read_only","affected_resources":["astronomer"],"recommended_capability":"astronomer.management.workload_restart"}}`,
		`{"finding":{"finding_id":"finding-a","session_id":"session-a","block_code":"read_only","affected_resources":["sha256:` + digest + `"],"recommended_capability":"invalid capability"}}`,
	} {
		if _, err := bridgeFindingScopeFromEnvelope(decodeFindingEnvelopeForTest(t, json.RawMessage(raw))); err == nil {
			t.Fatalf("unsafe finding scope was accepted: %s", raw)
		}
	}
}

func decodeFindingEnvelopeForTest(t *testing.T, raw json.RawMessage) contract.FindingEnvelope {
	t.Helper()
	var envelope contract.FindingEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Schema = "charlie.finding/v1"
	return envelope
}

func TestExactFindingResourceRejectsMissingAndAmbiguousDigests(t *testing.T) {
	resources := []sqlc.CharlieSessionResource{
		{ResourceType: "self_management_application", ResourceID: "astronomer", RequiredVerb: "read"},
		{ResourceType: "cluster_agents", ResourceID: "cluster-agents", RequiredVerb: "read"},
	}
	got, ok := exactFindingResource(resources, findingResourceDigest("astronomer"))
	if !ok || got.ResourceType != "self_management_application" {
		t.Fatalf("exact resource = %#v, %v", got, ok)
	}
	if _, ok := exactFindingResource(resources, findingResourceDigest("missing")); ok {
		t.Fatal("missing resource digest was accepted")
	}
	resources = append(resources, sqlc.CharlieSessionResource{ResourceType: "installation", ResourceID: "astronomer", RequiredVerb: "read"})
	if _, ok := exactFindingResource(resources, findingResourceDigest("astronomer")); ok {
		t.Fatal("ambiguous resource digest was accepted")
	}
}

func TestCentralFindingWorkflowRejectsInconsistentAuthorityLabels(t *testing.T) {
	base := BridgeFindingSummary{Status: "open", WorkflowState: "manual_remediation_required", BlockCode: "read_only"}
	if !validCentralFindingWorkflow(base) {
		t.Fatal("valid manual workflow rejected")
	}
	if !validCentralFindingWorkflow(BridgeFindingSummary{Status: "reopened", WorkflowState: "approval_pending", BlockCode: "approval_required"}) {
		t.Fatal("reopened approval-required recurrence was rejected")
	}
	for _, unsafe := range []BridgeFindingSummary{
		{Status: "open", WorkflowState: "approval_pending", BlockCode: "read_only"},
		{Status: "open", WorkflowState: "verification_pending", BlockCode: "verification_failed"},
		{Status: "resolved", WorkflowState: "rejected", BlockCode: "read_only"},
		{Status: "open", WorkflowState: "resolved", BlockCode: "read_only"},
	} {
		if validCentralFindingWorkflow(unsafe) {
			t.Fatalf("inconsistent central workflow accepted: %#v", unsafe)
		}
	}
}
