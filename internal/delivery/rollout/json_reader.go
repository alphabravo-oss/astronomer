package rollout

import "github.com/alphabravocompany/astronomer-go/internal/jsoncontract"

// ReadFrozenRolloutV1 is the existing exact frozen-plan schema, shared with the
// read-only integrity inventory. Indexed row identity is checked separately by
// the PostgreSQL reader; syntactic validity never grants tenant authorization.
func ReadFrozenRolloutV1(raw []byte) (FrozenRollout, error) {
	var plan FrozenRollout
	if len(raw) > jsoncontract.MaxDocumentBytes {
		return plan, fail(CodeInvariant, "frozen_plan", "stored plan exceeds the durable bound")
	}
	if err := decodeStrict(raw, &plan); err != nil {
		return FrozenRollout{}, fail(CodeInvariant, "frozen_plan", "stored plan is invalid")
	}
	if err := plan.Validate(); err != nil {
		return FrozenRollout{}, &Error{Code: CodeInvariant, Field: "frozen_plan", Cause: err}
	}
	return plan, nil
}
