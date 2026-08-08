package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration151AddsDurableIdempotentFindingDecisions(t *testing.T) {
	up, err := os.ReadFile("151_charlie_finding_decisions.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(up)
	for _, required := range []string{
		"CREATE TABLE charlie_finding_decisions",
		"request_id              UUID PRIMARY KEY",
		"finding_id              UUID NOT NULL REFERENCES charlie_findings(id) ON DELETE CASCADE",
		"actor_ref               VARCHAR(44) NOT NULL",
		"charlie_finding_decision_actor_ref",
		"charlie_finding_decision_value",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("migration 151 missing %q", required)
		}
	}
	down, err := os.ReadFile("151_charlie_finding_decisions.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS charlie_finding_decisions") {
		t.Fatal("migration 151 down does not remove the decision ledger")
	}
}

func TestFindingTransitionUsesExplicitTypesAndAtomicDecisionLedger(t *testing.T) {
	query, err := os.ReadFile("../queries/charlie.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(query)
	for _, required := range []string{
		"SET status = sqlc.arg(next_status)::varchar",
		"workflow_state = sqlc.arg(next_workflow_state)::varchar",
		"WITH transitioned AS (",
		"INSERT INTO charlie_finding_decisions",
		"SELECT request_id FROM recorded",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("finding transition query missing %q", required)
		}
	}
}
