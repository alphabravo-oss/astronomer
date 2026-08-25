package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestSIEMTestOperationMigrationPinsTerminalReceiptContract(t *testing.T) {
	raw, err := os.ReadFile("024_siem_test_operations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, required := range []string{
		"queue_id bigint UNIQUE",
		"UNIQUE (idempotency_scope, idempotency_key)",
		"request_digest char(64) NOT NULL",
		"status IN ('pending', 'succeeded', 'failed')",
		"completed_at timestamptz",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("SIEM test operation migration missing %q", required)
		}
	}
}
