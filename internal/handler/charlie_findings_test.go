package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type charlieFindingAccessFake struct {
	views   []charlie.FindingView
	actor   uuid.UUID
	finding uuid.UUID
	request uuid.UUID
	next    string
}

func (f *charlieFindingAccessFake) List(_ context.Context, actor uuid.UUID, _ string, _, _ int32) ([]charlie.FindingView, error) {
	f.actor = actor
	return f.views, nil
}
func (f *charlieFindingAccessFake) Get(_ context.Context, actor, finding uuid.UUID) (charlie.FindingView, error) {
	f.actor, f.finding = actor, finding
	return f.views[0], nil
}
func (f *charlieFindingAccessFake) Transition(_ context.Context, actor, finding, request uuid.UUID, next string) (charlie.FindingView, error) {
	f.actor, f.finding, f.request, f.next = actor, finding, request, next
	view := f.views[0]
	view.Finding.Status = next
	return view, nil
}

func withFindingParam(r *http.Request, findingID uuid.UUID) *http.Request {
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("finding_id", findingID.String())
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, routeContext))
}

func findingHandlerFixture() (*CharlieFindingHandler, *charlieFindingAccessFake, uuid.UUID, uuid.UUID) {
	actor, findingID := uuid.New(), uuid.New()
	access := &charlieFindingAccessFake{views: []charlie.FindingView{{
		Finding:   sqlc.CharlieFinding{ID: findingID, Title: "Tunnel imbalance", Severity: "warning", Status: "open", WorkflowState: "manual_remediation_required", Summary: "Review replica distribution", ExecutionBlockCode: "read_only", RepeatCount: 2, UpdatedAt: time.Unix(100, 0)},
		Resources: []sqlc.CharlieFindingResource{{FindingID: findingID, ResourceType: "tunnel", ResourceID: "replica-a", RequiredVerb: "read"}},
	}}}
	return NewCharlieFindingHandler(access), access, actor, findingID
}

func TestCharlieFindingListReturnsOnlyBoundedProductSummary(t *testing.T) {
	h, access, actor, _ := findingHandlerFixture()
	request := authenticatedCharlieRequest(http.MethodGet, "/api/v1/charlie/findings/", "", actor, "jwt")
	recorder := httptest.NewRecorder()
	h.List(recorder, request)
	if recorder.Code != http.StatusOK || access.actor != actor {
		t.Fatalf("status=%d actor=%s body=%s", recorder.Code, access.actor, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{`"severity":"medium"`, `"affected_resource"`, `"reason_no_action":"read_only"`,
		`"workflow_state":"manual_remediation_required"`, `"available_decisions":["acknowledge","start_remediation","dismiss"]`} {
		if !strings.Contains(body, want) {
			t.Fatalf("bounded finding response missing %s: %s", want, body)
		}
	}
	for _, prohibited := range []string{"charlie_finding_id", "dedupe_fingerprint", "authorization_ref", "connection_id"} {
		if strings.Contains(body, prohibited) {
			t.Fatalf("finding response exposed %q: %s", prohibited, body)
		}
	}
}

func TestSafeCharlieFindingApprovalExposesOnlyExactApprovalDecisions(t *testing.T) {
	now := time.Now().UTC()
	view := charlie.FindingView{Finding: sqlc.CharlieFinding{
		ID: uuid.New(), Title: "Approval required", Severity: "warning", Status: "open",
		EffectiveMode: "approval", ExecutionBlockCode: "approval_required",
		ApprovalID: pgtype.Text{String: "approval-safe-id", Valid: true},
		ExpiresAt:  pgtype.Timestamptz{Time: now.Add(time.Minute), Valid: true}, UpdatedAt: now,
	}}
	raw, err := json.Marshal(safeCharlieFinding(view, true))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{`"workflow_state":"approval_pending"`, `"open_exact_approval"`, `"reject_exact_approval"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("exact approval workflow missing %s: %s", want, body)
		}
	}
	for _, forbidden := range []string{`"acknowledge"`, `"dismiss"`, `"resolve"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("approval workflow exposed invalid decision %s: %s", forbidden, body)
		}
	}
}

func TestSafeCharlieFindingTerminalWorkflowReturnsAnEmptyDecisionArray(t *testing.T) {
	view := charlie.FindingView{Finding: sqlc.CharlieFinding{
		ID: uuid.New(), Title: "Resolved", Severity: "medium", Status: "resolved", UpdatedAt: time.Now().UTC(),
	}}
	raw, err := json.Marshal(safeCharlieFinding(view, false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"workflow_state":"resolved"`) || !strings.Contains(string(raw), `"available_decisions":[]`) {
		t.Fatalf("terminal workflow contract is not an empty decision array: %s", raw)
	}
}

func TestCharlieFindingTransitionRequiresStableRequestID(t *testing.T) {
	h, access, actor, findingID := findingHandlerFixture()
	requestID := uuid.New()
	request := authenticatedCharlieRequest(http.MethodPost, "/", `{"request_id":"`+requestID.String()+`"}`, actor, "jwt")
	request = withFindingParam(request, findingID)
	recorder := httptest.NewRecorder()
	h.Acknowledge(recorder, request)
	if recorder.Code != http.StatusOK || access.actor != actor || access.finding != findingID || access.request != requestID || access.next != "acknowledge" {
		t.Fatalf("transition was not forwarded exactly: status=%d access=%#v", recorder.Code, access)
	}

	bad := authenticatedCharlieRequest(http.MethodPost, "/", `{}`, actor, "jwt")
	bad = withFindingParam(bad, findingID)
	recorder = httptest.NewRecorder()
	h.Resolve(recorder, bad)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency request id status=%d", recorder.Code)
	}
}

func TestCharlieFindingEndpointsRejectAPITokenAuthentication(t *testing.T) {
	h, _, actor, findingID := findingHandlerFixture()
	request := authenticatedCharlieRequest(http.MethodGet, "/", "", actor, "api_token")
	request = withFindingParam(request, findingID)
	recorder := httptest.NewRecorder()
	h.Get(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("api token finding access status=%d", recorder.Code)
	}
}

func TestSafeCharlieFindingExposesOnlyApprovalLinkageNotAuthorityMaterial(t *testing.T) {
	now := time.Now().UTC()
	view := charlie.FindingView{Finding: sqlc.CharlieFinding{
		ID: uuid.New(), Title: "Approval required", Severity: "warning", Status: "open",
		EffectiveMode: "approval", ExecutionBlockCode: "approval_required",
		ApprovalID:             pgtype.Text{String: "approval-safe-id", Valid: true},
		RecommendedActionLabel: "Review exact approval",
		ExpiresAt:              pgtype.Timestamptz{Time: now.Add(time.Minute), Valid: true}, UpdatedAt: now,
	}}
	raw, err := json.Marshal(safeCharlieFinding(view, true))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `"approval_id":"approval-safe-id"`) || !strings.Contains(body, `"eligible":true`) {
		t.Fatalf("approval linkage missing: %s", body)
	}
	for _, forbidden := range []string{"manifest", "signature", "argument_digest", "authorization_ref", "disclosure_digest"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("finding exposed approval authority %q: %s", forbidden, body)
		}
	}
}
