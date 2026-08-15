package charlie

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type actionAuditQueriesFake struct{ rows []sqlc.CreateAuditLogV1Params }

func (f *actionAuditQueriesFake) CreateAuditLogV1(_ context.Context, row sqlc.CreateAuditLogV1Params) error {
	f.rows = append(f.rows, row)
	return nil
}

func TestDBActionAuditorPersistsPreDispatchDenialWithoutDescriptorEffect(t *testing.T) {
	queries := &actionAuditQueriesFake{}
	auditor, err := NewDBActionAuditor(queries)
	if err != nil {
		t.Fatal(err)
	}
	envelope := ActionEnvelope{
		ActionID: "action-a", AuthorizationRef: "authorization-a",
		Capability: "astronomer.management.kubernetes.workloads", ArgumentDigest: "arguments-a",
		ModeRevision: 2, PolicyRevision: 3, FencingEpoch: 4,
	}
	result := ActionResult{State: "denied", Code: DeniedScope}
	if err := auditor.Record(context.Background(), "denied", envelope, CapabilityDescriptor{}, result); err != nil {
		t.Fatal(err)
	}
	if len(queries.rows) != 1 {
		t.Fatalf("audit rows=%d", len(queries.rows))
	}
	row := queries.rows[0]
	var detail map[string]any
	if err := json.Unmarshal(row.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if _, ok := detail["effect"]; ok {
		t.Fatalf("pre-dispatch denial invented an effect: %s", row.Detail)
	}
	if detail["denial_code"] != string(DeniedScope) || row.StatusCode != 403 || row.ActionClass != "mutation" {
		t.Fatalf("pre-dispatch denial audit is incomplete: row=%+v detail=%s", row, row.Detail)
	}
}

func TestDBActionAuditorPersistsContentFreeDigests(t *testing.T) {
	queries := &actionAuditQueriesFake{}
	auditor, err := NewDBActionAuditor(queries)
	if err != nil {
		t.Fatal(err)
	}
	envelope := ActionEnvelope{
		ActionID: "SENTINEL-action", AuthorizationRef: "SENTINEL-authorization",
		Capability: "SENTINEL-capability", ArgumentDigest: "SENTINEL-arguments",
		ModeRevision: 2, PolicyRevision: 3, FencingEpoch: 4,
	}
	result := ActionResult{Allowed: true, State: "succeeded", Result: []byte(`{"secret":"SENTINEL-result"}`)}
	if err := auditor.Record(context.Background(), "succeeded", envelope, CapabilityDescriptor{Effect: EffectWrite}, result); err != nil {
		t.Fatal(err)
	}
	if len(queries.rows) != 1 {
		t.Fatalf("audit rows=%d", len(queries.rows))
	}
	row := queries.rows[0]
	if row.Path != "" {
		t.Fatalf("Charlie audit retained HTTP/MCP path: %q", row.Path)
	}
	serialized := row.CorrelationID + row.ResourceID + string(row.Detail)
	if strings.Contains(serialized, "SENTINEL") || strings.Contains(serialized, `"secret"`) {
		t.Fatalf("audit leaked action content: %s", serialized)
	}
	for _, field := range []string{"action_digest", "argument_digest", "authorization_digest", "capability_digest", "result_digest"} {
		if !strings.Contains(string(row.Detail), field) {
			t.Fatalf("audit lacks %s: %s", field, row.Detail)
		}
	}
}
