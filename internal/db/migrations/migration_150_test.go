package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestMigration150PersistsOneBoundedCharlieFindingWorkflow(t *testing.T) {
	upBytes, err := os.ReadFile("150_charlie_finding_workflow_state.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := string(upBytes)
	for _, required := range []string{
		"ADD COLUMN workflow_state VARCHAR(32) NOT NULL",
		"approval_pending",
		"manual_remediation_required",
		"remediation_in_progress",
		"verification_pending",
		"approval_id IS NOT NULL",
		"expires_at > now()",
		"charlie_finding_workflow_consistency",
		"Platform Operator",
		"Charlie Approver",
		`["update"]`,
	} {
		if !strings.Contains(up, required) {
			t.Errorf("migration 150 missing %q", required)
		}
	}
	if strings.Contains(strings.ToLower(up), "delete from") {
		t.Fatal("finding workflow migration must preserve existing findings")
	}

	downBytes, err := os.ReadFile("150_charlie_finding_workflow_state.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(downBytes), "DROP COLUMN IF EXISTS workflow_state") {
		t.Fatal("migration 150 down does not remove its workflow column")
	}
}
