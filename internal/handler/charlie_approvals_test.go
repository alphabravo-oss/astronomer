package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type charlieApprovalAccessFake struct {
	views     []charlie.ApprovalView
	actor     uuid.UUID
	approval  string
	request   uuid.UUID
	decision  string
	rationale string
	err       error
}

func (f *charlieApprovalAccessFake) List(_ context.Context, actor uuid.UUID) ([]charlie.ApprovalView, error) {
	f.actor = actor
	return f.views, nil
}

func (f *charlieApprovalAccessFake) Decide(_ context.Context, actor uuid.UUID, approval string, request uuid.UUID, decision, rationale string) (charlie.ApprovalView, error) {
	f.actor, f.approval, f.request, f.decision, f.rationale = actor, approval, request, decision, rationale
	if f.err != nil {
		return charlie.ApprovalView{}, f.err
	}
	view := f.views[0]
	view.State = map[string]string{"approve": "approved", "reject": "denied"}[decision]
	return view, nil
}

func withApprovalParam(r *http.Request, approvalID string) *http.Request {
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("approval_id", approvalID)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, routeContext))
}

func TestCharlieApprovalListReturnsNoAuthorityMaterial(t *testing.T) {
	actor := uuid.New()
	access := &charlieApprovalAccessFake{views: []charlie.ApprovalView{{
		ID: "approval-a", Title: "Approve bounded retry", State: "pending", Eligible: true,
		Capability: "astronomer.queue.retry_task", Target: "management_component:task-a", Risk: "medium",
		Effect: "retry one failed task", RequiredPermission: "management_components:astronomer.queue.retry_task",
		ExpiresAt: time.Date(2026, 8, 5, 10, 5, 0, 0, time.UTC),
	}}}
	recorder := httptest.NewRecorder()
	NewCharlieApprovalHandler(access).List(recorder, authenticatedCharlieRequest(http.MethodGet, "/", "", actor, "jwt"))
	if recorder.Code != http.StatusOK || access.actor != actor {
		t.Fatalf("status=%d actor=%s body=%s", recorder.Code, access.actor, recorder.Body.String())
	}
	for _, prohibited := range []string{"manifest", "signature", "argument_digest", "authorization_ref", "disclosure_digest"} {
		if strings.Contains(recorder.Body.String(), prohibited) {
			t.Fatalf("approval response exposed %q: %s", prohibited, recorder.Body.String())
		}
	}
	for _, required := range []string{"retry one failed task", "management_components:astronomer.queue.retry_task"} {
		if !strings.Contains(recorder.Body.String(), required) {
			t.Fatalf("approval response omitted %q: %s", required, recorder.Body.String())
		}
	}
}

func TestCharlieApprovalDecisionRequiresStableRequestIDAndMapsDeny(t *testing.T) {
	actor, requestID := uuid.New(), uuid.New()
	access := &charlieApprovalAccessFake{views: []charlie.ApprovalView{{ID: "approval-a", State: "pending"}}}
	handler := NewCharlieApprovalHandler(access)
	request := authenticatedCharlieRequest(http.MethodPost, "/", `{"request_id":"`+requestID.String()+`","decision":"deny","rationale":"unsafe now"}`, actor, "jwt")
	request = withApprovalParam(request, "approval-a")
	recorder := httptest.NewRecorder()
	handler.Decide(recorder, request)
	if recorder.Code != http.StatusOK || access.actor != actor || access.approval != "approval-a" || access.request != requestID || access.decision != "reject" || access.rationale != "unsafe now" {
		t.Fatalf("decision was not forwarded exactly: status=%d access=%#v body=%s", recorder.Code, access, recorder.Body.String())
	}

	bad := withApprovalParam(authenticatedCharlieRequest(http.MethodPost, "/", `{"decision":"approve"}`, actor, "jwt"), "approval-a")
	recorder = httptest.NewRecorder()
	handler.Decide(recorder, bad)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency key status=%d", recorder.Code)
	}
}

func TestCharlieApprovalEndpointsRejectAPITokenAuthentication(t *testing.T) {
	actor := uuid.New()
	access := &charlieApprovalAccessFake{}
	recorder := httptest.NewRecorder()
	NewCharlieApprovalHandler(access).List(recorder, authenticatedCharlieRequest(http.MethodGet, "/", "", actor, "api_token"))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("API token approval access status=%d", recorder.Code)
	}
}

func TestCharlieApprovalStaleDecisionReturnsConflict(t *testing.T) {
	access := &charlieApprovalAccessFake{err: errors.New("approval is stale and no longer pending")}
	request := withApprovalParam(authenticatedCharlieRequest(http.MethodPost, "/", `{"request_id":"`+uuid.NewString()+`","decision":"approve"}`, uuid.New(), "jwt"), "approval-a")
	recorder := httptest.NewRecorder()
	NewCharlieApprovalHandler(access).Decide(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("stale approval status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
