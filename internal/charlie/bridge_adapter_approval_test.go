package charlie

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestProductBridgeApprovalDecisionBindsActorAndTrimmedRationale(t *testing.T) {
	actorID, requestID := uuid.New(), uuid.New()
	request, err := productBridgeApprovalDecision(BridgeApprovalDecision{
		RequestID: requestID, Decision: "approve", DecidedBy: "user:" + actorID.String(),
		Rationale: "  reviewed exact impact  \n", ManifestDigest: strings.Repeat("a", 64),
	}, "opaque-authorization-ref")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["decided_by"] != "user:"+actorID.String() || fields["rationale"] != "reviewed exact impact" ||
		fields["request_id"] != requestID.String() || fields["authorization_ref"] != "opaque-authorization-ref" {
		t.Fatalf("approval decision wire fields = %#v", fields)
	}
}

func TestProductBridgeApprovalDecisionOmitsEmptyRationaleAndRejectsSpoofedActor(t *testing.T) {
	base := BridgeApprovalDecision{
		RequestID: uuid.New(), Decision: "reject", DecidedBy: "user:" + uuid.NewString(),
		ManifestDigest: strings.Repeat("b", 64),
	}
	request, err := productBridgeApprovalDecision(base, "opaque-authorization-ref")
	if err != nil || request.Rationale != nil {
		t.Fatalf("empty rationale was not omitted: request=%#v err=%v", request, err)
	}
	base.DecidedBy = "service:admin"
	if _, err := productBridgeApprovalDecision(base, "opaque-authorization-ref"); err == nil {
		t.Fatal("non-user decision actor was accepted")
	}
	base.DecidedBy = "user:" + uuid.NewString()
	base.Rationale = strings.Repeat("x", 513)
	if _, err := productBridgeApprovalDecision(base, "opaque-authorization-ref"); err == nil {
		t.Fatal("oversized rationale was accepted")
	}
}
