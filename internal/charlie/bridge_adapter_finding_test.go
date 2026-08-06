package charlie

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestDecodeBridgeFindingScopeKeepsOnlyExactActionMetadata(t *testing.T) {
	digest := findingResourceDigest("astronomer")
	raw := json.RawMessage(`{"finding":{"finding_id":"finding-a","session_id":"session-a","block_code":"read_only","affected_resources":["sha256:` + digest + `"],"recommended_capability":"astronomer.argocd.self_management_sync","diagnosis":"content-canary","operator_checks":["content-canary"]}}`)
	got, err := decodeBridgeFindingScope(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.FindingID != "finding-a" || got.SessionID != "session-a" || got.ResourceDigest != digest || got.RecommendedCapability != "astronomer.argocd.self_management_sync" {
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
		`{"finding":{"finding_id":"finding-a","session_id":"session-a","block_code":"read_only","affected_resources":[],"recommended_capability":"astronomer.argocd.self_management_sync"}}`,
		`{"finding":{"finding_id":"finding-a","session_id":"session-a","block_code":"read_only","affected_resources":["sha256:` + digest + `","sha256:` + digest + `"],"recommended_capability":"astronomer.argocd.self_management_sync"}}`,
		`{"finding":{"finding_id":"finding-a","session_id":"session-a","block_code":"read_only","affected_resources":["astronomer"],"recommended_capability":"astronomer.argocd.self_management_sync"}}`,
		`{"finding":{"finding_id":"finding-a","session_id":"session-a","block_code":"read_only","affected_resources":["sha256:` + digest + `"],"recommended_capability":"invalid capability"}}`,
	} {
		if _, err := decodeBridgeFindingScope(json.RawMessage(raw)); err == nil {
			t.Fatalf("unsafe finding scope was accepted: %s", raw)
		}
	}
}

func TestExactFindingResourceRejectsMissingAndAmbiguousDigests(t *testing.T) {
	resources := []sqlc.CharlieSessionResource{
		{ResourceType: "self_management_application", ResourceID: "astronomer", RequiredVerb: "read"},
		{ResourceType: "agent_fleet", ResourceID: "fleet", RequiredVerb: "read"},
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
