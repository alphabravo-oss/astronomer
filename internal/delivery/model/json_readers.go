package model

import (
	"encoding/json"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

// ReadPlacementV1 retains additive v1 properties and legacy omitted/null
// optional selector collections. Invalid envelopes and domain selectors return
// a content-free integrity category, rather than a usable zero-value selector.
func ReadPlacementV1(raw []byte) (Placement, error) {
	var value Placement
	if _, err := jsoncontract.ReadObjectV1("placement", raw); err != nil {
		return value, err
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return Placement{}, &jsoncontract.Error{Field: "placement", Category: "invalid_item"}
	}
	if err := value.Validate(); err != nil {
		return Placement{}, &jsoncontract.Error{Field: "placement", Category: "invalid_domain"}
	}
	return value, nil
}

// ReadRolloutStrategyV1 uses the same typed scheduling validator as new writes.
// Additive properties remain compatible; unknown strategy values do not become
// scheduling defaults. Callers must propagate the integrity error.
func ReadRolloutStrategyV1(raw []byte) (RolloutStrategy, error) {
	var value RolloutStrategy
	if _, err := jsoncontract.ReadObjectV1("rollout_strategy", raw); err != nil {
		return value, err
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return RolloutStrategy{}, &jsoncontract.Error{Field: "rollout_strategy", Category: "invalid_item"}
	}
	if err := value.Validate(); err != nil {
		return RolloutStrategy{}, &jsoncontract.Error{Field: "rollout_strategy", Category: "invalid_domain"}
	}
	return value, nil
}
