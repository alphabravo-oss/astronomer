package charlie

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestAdvisoryAndNotificationDTOsHaveNoExecutionFields(t *testing.T) {
	forbidden := map[string]struct{}{
		"manifest": {}, "signature": {}, "authorization_ref": {}, "arguments": {},
		"argument_digest": {}, "action_request": {}, "request_id": {}, "approval_id": {},
	}
	for _, value := range []any{
		FindingAlert{}, FindingAdvisoryDetail{}, FindingManualRemediation{}, FindingVerification{}, BridgeFindingSummary{},
	} {
		typeOf := reflect.TypeOf(value)
		for index := 0; index < typeOf.NumField(); index++ {
			field := typeOf.Field(index)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if _, unsafe := forbidden[name]; unsafe {
				t.Fatalf("%s structurally admits execution field %q", typeOf.Name(), name)
			}
		}
	}
}

func TestInternalFindingViewCannotBeSerializedAsAnAuthorityPayload(t *testing.T) {
	view := FindingView{Finding: sqlc.CharlieFinding{ApprovalID: pgtype.Text{String: "approval-private", Valid: true}}}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "{}" {
		t.Fatalf("internal finding view became a response payload: %s", encoded)
	}
}

func TestFindingEnvelopeIsReducedToBoundedAdvisoryWithoutApprovalLinkage(t *testing.T) {
	var envelope contract.FindingEnvelope
	raw := []byte(`{
		"schema":"charlie.finding/v1",
		"finding":{
			"finding_id":"finding-a","deduplication_key":"0000000000000000000000000000000000000000000000000000000000000000",
			"severity":"medium","status":"open","workflow":{"state":"approval_pending","approval_id":"approval-forged-link"},
			"affected_resources":["resource-a"],"evidence_summary":["bounded evidence"],"diagnosis":"bounded diagnosis","confidence":0.8,
			"block_code":"approval_required","risk_impact":"bounded impact","operator_checks":["check resource"],
			"verification_steps":["re-read resource"],"investigation_id":"investigation-a","session_id":"session-a",
			"created_at":"2026-08-06T00:00:00Z","updated_at":"2026-08-06T00:00:00Z"
		},
		"lifecycle":[{"event_id":"event-a","transition":"created","workflow_state":"approval_pending","actor_ref":"actor-a","occurred_at":"2026-08-06T00:00:00Z"}],
		"storage":{"encryption_required":true,"retention_days":30,"expires_at":"2026-09-05T00:00:00Z"}
	}`)
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	detail, err := advisoryDetailFromEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"diagnosis":"bounded diagnosis"`) || strings.Contains(string(encoded), "approval") {
		t.Fatalf("finding envelope was not reduced to advisory content: %s", encoded)
	}
	envelope.Finding.Diagnosis = strings.Repeat("x", 4097)
	if _, err := advisoryDetailFromEnvelope(envelope); err == nil {
		t.Fatal("oversized advisory content was accepted")
	}
}

func TestForgedExecutionFieldsCannotSurviveTypedAdvisoryPayloads(t *testing.T) {
	forged := []string{"manifest", "signature", "authorization_ref", "arguments", "argument_digest", "action_request", "request_id", "approval_id"}
	for _, field := range forged {
		t.Run(field, func(t *testing.T) {
			raw := []byte(`{"diagnosis":"bounded","risk_impact":"bounded","operator_checks":[],"verification_steps":[],"` + field + `":{"forged":true}}`)
			var detail FindingAdvisoryDetail
			if err := json.Unmarshal(raw, &detail); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(detail)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if _, exists := fields[field]; exists {
				t.Fatalf("forged execution field survived typed advisory: %s", encoded)
			}
		})
	}
}
