package sqlc

import (
	"strings"
	"testing"
)

// The incident budget is intentionally session-local, but a new trigger
// session must not reset either the deployment window budget or the resource
// cooldown. The policy-row lock is the cross-session serialization point.
func TestCharlieAutoBudgetWindowAndCooldownAreDeploymentScoped(t *testing.T) {
	for name, query := range map[string]string{
		"snapshot": getCharlieActionSafetySnapshot,
		"reserve":  reserveCharlieAutoBudget,
	} {
		normalized := strings.Join(strings.Fields(query), " ")
		if !strings.Contains(normalized, "other.connection_id = r.connection_id") &&
			!strings.Contains(normalized, "s.connection_id = current_session.connection_id") {
			t.Fatalf("%s query does not scope window/cooldown across the deployment connection", name)
		}
		if !strings.Contains(normalized, "other.capability = r.capability") &&
			!strings.Contains(normalized, "r.capability = $1") {
			t.Fatalf("%s query does not bind the deployment window to one capability", name)
		}
	}

	normalizedReserve := strings.Join(strings.Fields(reserveCharlieAutoBudget), " ")
	if !strings.Contains(normalizedReserve, "FOR UPDATE") ||
		!strings.Contains(normalizedReserve, "charlie_automation_policies") {
		t.Fatal("auto-budget reservation must lock the shared policy row before counting cross-session receipts")
	}
}
