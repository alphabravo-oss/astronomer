package charlie

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/downstreamboundary"
)

func TestProhibitedOperationsAreAbsentRejectedAuditedAndZeroEgress(t *testing.T) {
	prohibitedTools := []string{
		"astronomer.downstream.resource_list",
		"astronomer.downstream.agent_restart",
		"astronomer.kubernetes.exec",
		"astronomer.kubernetes.apply",
		"astronomer.management.workload_delete",
		"astronomer.secret.read",
		"astronomer.credentials.rotate",
		"astronomer.management.production_restore",
		"astronomer.sql.execute",
		"astronomer.http.request",
	}
	discovery, err := json.Marshal(mcpTools())
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range prohibitedTools {
		if strings.Contains(string(discovery), tool) {
			t.Fatalf("prohibited tool %s was disclosed", tool)
		}
		t.Run("tool_name/"+tool, func(t *testing.T) {
			publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			authority := &fakeLiveAuthority{facts: []AuthorityInput{allowedWriteFacts(ModeAuto)}}
			executor := &fakeCapabilityExecutor{}
			auditor := &fakeActionAuditor{}
			guard, err := NewActionGuard(publicKey, authority, &fakeReceipts{}, executor, auditor)
			if err != nil {
				t.Fatal(err)
			}
			before := downstreamboundary.TakeSnapshot()
			result := guard.Execute(context.Background(), signedTestAction(t, privateKey, tool, map[string]any{
				"resource_id": "downstream-SENTINEL", "operation_id": "action-a", "raw_request": "SENTINEL",
			}))
			if result.Code != DeniedScope || authority.calls != 0 || executor.calls != 0 || stringSlice(auditor.phases) != stringSlice([]string{"proposed", "denied"}) {
				t.Fatalf("prohibited tool escaped guard: result=%+v authority=%d execute=%d audit=%v", result, authority.calls, executor.calls, auditor.phases)
			}
			if after := downstreamboundary.TakeSnapshot(); after.DeltaTotal(before) != 0 {
				t.Fatalf("prohibited tool crossed downstream boundary: %+v", after.Delta(before))
			}
		})
	}

	craftedFields := []struct {
		name  string
		value any
	}{
		{"command", "SENTINEL"}, {"shell", "SENTINEL"}, {"url", "https://downstream.invalid"},
		{"sql", "SENTINEL"}, {"gvr", "v1/pods"}, {"kubeconfig", "SENTINEL"},
		{"namespace", "downstream"}, {"raw_request", "SENTINEL"},
	}
	for _, descriptor := range append(ReadCapabilityCatalog(), WriteCapabilityCatalog()...) {
		descriptor := descriptor
		for _, field := range craftedFields {
			field := field
			t.Run("crafted_argument/"+descriptor.Name+"/"+field.name, func(t *testing.T) {
				arguments := validReadArguments(descriptor.Name)
				facts := allowedReadFacts()
				if descriptor.Effect == EffectWrite {
					arguments = validWriteArguments(descriptor.Name)
					facts = allowedWriteFacts(ModeAuto)
				}
				arguments[field.name] = field.value
				publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				authority := &fakeLiveAuthority{facts: []AuthorityInput{facts}}
				executor := &fakeCapabilityExecutor{}
				auditor := &fakeActionAuditor{}
				guard, err := NewActionGuard(publicKey, authority, &fakeReceipts{}, executor, auditor)
				if err != nil {
					t.Fatal(err)
				}
				before := downstreamboundary.TakeSnapshot()
				result := guard.Execute(context.Background(), signedTestAction(t, privateKey, descriptor.Name, arguments))
				if result.Code != DeniedScope || authority.calls != 0 || executor.calls != 0 || stringSlice(auditor.phases) != stringSlice([]string{"proposed", "denied"}) {
					t.Fatalf("crafted argument escaped guard: result=%+v authority=%d execute=%d audit=%v", result, authority.calls, executor.calls, auditor.phases)
				}
				if after := downstreamboundary.TakeSnapshot(); after.DeltaTotal(before) != 0 {
					t.Fatalf("crafted argument crossed downstream boundary: %+v", after.Delta(before))
				}
			})
		}
	}
}

// This matrix deliberately reuses the canonical scenario tests instead of
// substituting Charlie-local egress fakes. The counters live at the real
// management-plane tunnel/remotedialer boundaries, so any future production
// dependency that reaches a downstream agent changes the snapshot.
func TestCharlieScenarioMatrixNeverCrossesDownstreamBoundary(t *testing.T) {
	scenarios := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "user chat", run: TestPrivateSessionOwnerIsolationAndLiveRecheck},
		{name: "triggered investigation", run: TestTriggerDispatchCreatesOneIncidentSessionAndPublishesAfterCommit},
		{name: "every MCP read", run: TestProductionReadAdaptersExecuteEntireCatalogWithSafeBoundedShapes},
		{name: "approval action", run: TestApprovalAccessApprovesExactSignedActionOnce},
		{name: "auto action", run: TestActionGuardExecutesBoundedWriteOnceAndVerifies},
	}
	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			before := downstreamboundary.TakeSnapshot()
			scenario.run(t)
			after := downstreamboundary.TakeSnapshot()
			if delta := after.DeltaTotal(before); delta != 0 {
				t.Fatalf("Charlie scenario crossed a downstream tunnel/proxy boundary %d time(s): %+v", delta, after.Delta(before))
			}
		})
	}
}
