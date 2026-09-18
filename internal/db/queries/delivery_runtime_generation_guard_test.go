package queries

import (
	"os"
	"strings"
	"testing"
)

func TestDeliveryStatusAndRuntimeGenerationQueriesRemainFenced(t *testing.T) {
	raw, err := os.ReadFile("delivery.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"-- name: AcceptDeliveryStatusInventory :one",
		"delivery_controller_inventory.semantic_sequence = delivery_controller_inventory.agent_sequence AS status_changed",
		"delivery_controller_inventory.agent_sequence < EXCLUDED.agent_sequence",
		"r.runtime_generation",
		"runtime_generation = sqlc.arg(expected_runtime_generation)",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("delivery query contract is missing %q", required)
		}
	}
}
