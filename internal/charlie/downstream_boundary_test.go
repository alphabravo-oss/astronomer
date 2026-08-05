package charlie

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/downstreamboundary"
)

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
