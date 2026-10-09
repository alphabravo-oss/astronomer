package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
)

func TestRolloutInventoryValidatesSeparateStrategyAndFrozenPlan(t *testing.T) {
	raw, err := os.ReadFile("testdata/frozen_rollout_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rollout.ReadFrozenRolloutV1(raw); err != nil {
		t.Fatalf("v1 canonical planner fixture invalid: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, spec := range rowSpecs() {
		if spec.table != "delivery_rollouts" {
			continue
		}
		if err := spec.validate([][]byte{raw, []byte(`{}`)}); err == nil {
			t.Fatal("invalid separate strategy missed beside valid frozen plan")
		}
		if err := spec.validate([][]byte{raw, fields["strategy"]}); err != nil {
			t.Fatalf("valid separate strategy and frozen plan rejected: %v", err)
		}
		return
	}
	t.Fatal("rollout inventory owner missing")
}
