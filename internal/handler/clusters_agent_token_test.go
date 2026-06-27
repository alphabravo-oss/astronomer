package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// serveClusterAction wires a single chi route with the {id} URL param and
// runs the handler against a POST, returning the recorder.
func serveClusterAction(h http.HandlerFunc, id string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Post("/clusters/{id}/agent-token/action/", h)
	req := httptest.NewRequest(http.MethodPost, "/clusters/"+id+"/agent-token/action/", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestRotateAgentTokenSetsPendingAndAudits: the admin rotate endpoint sets
// rotation_pending_at (does NOT touch the live token), returns 202, and emits
// the agent.token.rotate.requested audit.
func TestRotateAgentTokenSetsPendingAndAudits(t *testing.T) {
	id := uuid.New()
	q := &clusterRegistryTestQuerier{agentTokenRotationRows: 1}
	h := NewClusterHandler(q)

	rec := serveClusterAction(h.RotateAgentToken, id.String())
	if rec.Code != http.StatusAccepted {
		t.Fatalf("rotate code = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if q.rotationPendingCalledID != id {
		t.Fatalf("rotation pending set for %s, want %s", q.rotationPendingCalledID, id)
	}
	if len(q.auditRows) != 1 || q.auditRows[0].Action != "agent.token.rotate.requested" {
		t.Fatalf("expected agent.token.rotate.requested audit, got %+v", q.auditRows)
	}
}

// TestRotateAgentTokenNoEligibleTokenIs409: 0 rows means either no active token
// OR a rotation is already in flight (the query gates on rotation_pending_at IS
// NULL AND previous_token_hash IS NULL). Either way the handler returns 409
// Conflict — re-triggering would risk a double-rotation lockout — and emits no
// audit.
func TestRotateAgentTokenNoEligibleTokenIs409(t *testing.T) {
	id := uuid.New()
	q := &clusterRegistryTestQuerier{agentTokenRotationRows: 0}
	h := NewClusterHandler(q)

	rec := serveClusterAction(h.RotateAgentToken, id.String())
	if rec.Code != http.StatusConflict {
		t.Fatalf("rotate with no eligible token code = %d, want 409", rec.Code)
	}
	if len(q.auditRows) != 0 {
		t.Fatalf("expected no audit when nothing rotated, got %+v", q.auditRows)
	}
}

// TestRevokeAgentTokenSetsRevokedAndAudits: the admin revoke endpoint sets
// revoked_at (wiring the previously-dead column), returns 200, and emits the
// agent.token.revoked audit. FAILS WITHOUT THE FIX (endpoint + query absent).
func TestRevokeAgentTokenSetsRevokedAndAudits(t *testing.T) {
	id := uuid.New()
	q := &clusterRegistryTestQuerier{agentTokenRevokeRows: 1}
	h := NewClusterHandler(q)

	rec := serveClusterAction(h.RevokeAgentToken, id.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if q.revokeCalledID != id {
		t.Fatalf("revoke called for %s, want %s", q.revokeCalledID, id)
	}
	if len(q.auditRows) != 1 || q.auditRows[0].Action != "agent.token.revoked" {
		t.Fatalf("expected agent.token.revoked audit, got %+v", q.auditRows)
	}
}

// TestRevokeAgentTokenNoTokenIs404 mirrors the rotate 0-rows path.
func TestRevokeAgentTokenNoTokenIs404(t *testing.T) {
	id := uuid.New()
	q := &clusterRegistryTestQuerier{agentTokenRevokeRows: 0}
	h := NewClusterHandler(q)

	rec := serveClusterAction(h.RevokeAgentToken, id.String())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("revoke with no token code = %d, want 404", rec.Code)
	}
	if len(q.auditRows) != 0 {
		t.Fatalf("expected no audit when nothing revoked, got %+v", q.auditRows)
	}
}

// TestRotateAgentTokenInvalidID returns 400 for a non-UUID path param.
func TestRotateAgentTokenInvalidID(t *testing.T) {
	q := &clusterRegistryTestQuerier{}
	h := NewClusterHandler(q)
	rec := serveClusterAction(h.RotateAgentToken, "not-a-uuid")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid id code = %d, want 400", rec.Code)
	}
}
