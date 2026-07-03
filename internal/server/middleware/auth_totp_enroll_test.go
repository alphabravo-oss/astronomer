package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// TestAuthOrTOTPEnrollChallenge proves the enrollment-lockout fix: an enroll-only
// challenge token authenticates the enroll routes (and is flagged enroll-only),
// while it is still rejected as a normal session credential elsewhere.
func TestAuthOrTOTPEnrollChallenge(t *testing.T) {
	jwtMgr := newTestJWTManager()
	userID := uuid.New()
	q := &fakeTokenUserQuerier{user: sqlc.User{ID: userID, IsActive: true, Email: "u@x.io", Username: "u"}}

	enrollToken, err := jwtMgr.GeneratePurposeToken(userID, auth.PurposeTOTPEnrollOnly, 5*time.Minute)
	if err != nil {
		t.Fatalf("mint enroll token: %v", err)
	}
	sessionToken, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("mint session token: %v", err)
	}

	var gotUser string
	var gotEnrollOnly bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, ok := GetAuthenticatedUser(r.Context()); ok {
			gotUser = u.ID
		}
		gotEnrollOnly = IsTOTPEnrollOnlyAuth(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	// 1. Enroll-only challenge is accepted on the enroll route + flagged.
	h := AuthOrTOTPEnrollChallenge(jwtMgr, q)(inner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/totp/enroll/start/", nil)
	req.Header.Set("Authorization", "Bearer "+enrollToken)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("enroll challenge should be accepted, got %d", rec.Code)
	}
	if gotUser != userID.String() || !gotEnrollOnly {
		t.Fatalf("expected enroll-only auth for user %s, got user=%s enrollOnly=%v", userID, gotUser, gotEnrollOnly)
	}

	// 2. A normal session token also works here but is NOT flagged enroll-only.
	gotEnrollOnly = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/auth/totp/enroll/start/", nil)
	req.Header.Set("Authorization", "Bearer "+sessionToken)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("session token should be accepted, got %d", rec.Code)
	}
	if gotEnrollOnly {
		t.Fatalf("session token must not be flagged enroll-only")
	}

	// 3. The enroll-only challenge is still rejected as a normal session
	//    credential (the standard RequireAuth path refuses all purpose tokens).
	rejectRec := httptest.NewRecorder()
	rejectReq := httptest.NewRequest(http.MethodGet, "/api/v1/whatever/", nil)
	rejectReq.Header.Set("Authorization", "Bearer "+enrollToken)
	RequireAuthWithQueries(jwtMgr, q)(inner).ServeHTTP(rejectRec, rejectReq)
	if rejectRec.Code != http.StatusUnauthorized {
		t.Fatalf("enroll challenge must be rejected as a session credential, got %d", rejectRec.Code)
	}
}
