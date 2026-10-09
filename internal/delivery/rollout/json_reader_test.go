package rollout

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

func TestReadFrozenRolloutV1ExactCompatibility(t *testing.T) {
	plan := testPlan(t, 1, testStrategy("rolling", 1), false)
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrozenRolloutV1(raw)
	if err != nil || got.ID != plan.ID || got.PlanDigest != plan.PlanDigest {
		t.Fatalf("existing v1 plan failed: %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object["schema_version"] = json.RawMessage(`999`)
	future, _ := json.Marshal(object)
	for _, invalid := range [][]byte{future, append(append([]byte(nil), raw...), []byte(` {}`)...), []byte(`null`), bytes.Repeat([]byte("x"), jsoncontract.MaxDocumentBytes+1)} {
		if _, err := ReadFrozenRolloutV1(invalid); !HasCode(err, CodeInvariant) {
			t.Fatalf("invalid/unsupported exact plan did not produce invariant error: %v", err)
		}
	}
}
