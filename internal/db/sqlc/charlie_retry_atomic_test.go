package sqlc

import (
	"strings"
	"testing"
)

func TestCharlieRetryPersistsEventTaskAndAuditInOneStatement(t *testing.T) {
	for _, required := range []string{
		"INSERT INTO charlie_trigger_events",
		"INSERT INTO task_outbox",
		"INSERT INTO audit_outbox",
		"CROSS JOIN outbox CROSS JOIN audit",
	} {
		if !strings.Contains(retryDeadCharlieTriggerEventWithOutbox, required) {
			t.Fatalf("Charlie retry statement missing atomic boundary %q", required)
		}
	}
}
