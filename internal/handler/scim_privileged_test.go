package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Findings #5/#6: SCIM must not be able to deactivate or mutate a
// superuser/staff account. Without the guard a SCIM client (Okta/Azure
// bearer token, or a compromised IdP) could PUT/PATCH/re-POST active:false
// against every superuser, flipping is_active AND revoking their live
// sessions — a silent, complete lockout of all platform admins that the
// DeleteUser guard was written to prevent. It could also silently rewrite a
// privileged account's email/username, enabling SSO email-based rebinding.
func TestSCIMPrivilegedUserProtected(t *testing.T) {
	adminID := uuid.New()
	staffID := uuid.New()
	bobID := uuid.New()
	q := &fakeSCIMQuerier{
		users: map[string]sqlc.User{
			"admin": {ID: adminID, Username: "admin", Email: "admin@example.com", IsActive: true, IsSuperuser: true},
			"staff": {ID: staffID, Username: "staff", Email: "staff@example.com", IsActive: true, IsStaff: true},
			"bob":   {ID: bobID, Username: "bob", Email: "bob@example.com", IsActive: true},
		},
	}
	h := NewSCIMHandler(q)

	assertForbidden := func(t *testing.T, name string, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: want 403, got %d (%s)", name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "privileged user") {
			t.Fatalf("%s: unexpected body: %s", name, rec.Body.String())
		}
	}

	// --- PUT active:false against a superuser must be refused ---
	putReq := withChiID(httptest.NewRequest(http.MethodPut, "/scim/v2/Users/"+adminID.String(),
		strings.NewReader(`{"userName":"admin","active":false}`)), adminID.String())
	putRec := httptest.NewRecorder()
	h.PutUser(putRec, putReq)
	assertForbidden(t, "PUT superuser", putRec)

	// --- PATCH replace active:false against a superuser must be refused ---
	patchReq := withChiID(httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/"+adminID.String(),
		strings.NewReader(`{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"active","value":false}]}`)), adminID.String())
	patchRec := httptest.NewRecorder()
	h.PatchUser(patchRec, patchReq)
	assertForbidden(t, "PATCH superuser", patchRec)

	// --- PUT against a staff account must be refused too ---
	putStaffReq := withChiID(httptest.NewRequest(http.MethodPut, "/scim/v2/Users/"+staffID.String(),
		strings.NewReader(`{"userName":"staff","active":false}`)), staffID.String())
	putStaffRec := httptest.NewRecorder()
	h.PutUser(putStaffRec, putStaffReq)
	assertForbidden(t, "PUT staff", putStaffRec)

	// --- POST re-provision (update-in-disguise) of a superuser must be refused ---
	postReq := httptest.NewRequest(http.MethodPost, "/scim/v2/Users",
		strings.NewReader(`{"userName":"admin","active":false}`))
	postRec := httptest.NewRecorder()
	h.CreateUser(postRec, postReq)
	assertForbidden(t, "POST re-provision superuser", postRec)

	// The privileged accounts must remain active and their sessions intact.
	if !q.users["admin"].IsActive {
		t.Fatal("superuser was deactivated despite the guard")
	}
	if !q.users["staff"].IsActive {
		t.Fatal("staff account was deactivated despite the guard")
	}
	if len(q.invalidatedTokensFor) != 0 {
		t.Fatalf("privileged sessions were revoked despite the guard: %v", q.invalidatedTokensFor)
	}

	// --- Legit caller still works: a normal user CAN be deactivated, and
	//     the deactivation revokes their live sessions as before. ---
	bobReq := withChiID(httptest.NewRequest(http.MethodPut, "/scim/v2/Users/"+bobID.String(),
		strings.NewReader(`{"userName":"bob","active":false}`)), bobID.String())
	bobRec := httptest.NewRecorder()
	h.PutUser(bobRec, bobReq)
	if bobRec.Code != http.StatusOK {
		t.Fatalf("PUT normal user: want 200, got %d (%s)", bobRec.Code, bobRec.Body.String())
	}
	if q.users["bob"].IsActive {
		t.Fatal("normal user should have been deactivated")
	}
	found := false
	for _, id := range q.invalidatedTokensFor {
		if id == bobID {
			found = true
		}
	}
	if !found {
		t.Fatal("normal-user deactivation should have revoked their sessions")
	}
}
