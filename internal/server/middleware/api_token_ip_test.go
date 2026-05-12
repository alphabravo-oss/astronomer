package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeTokenUserQuerier is a hand-rolled stub the test wires into
// AuthWithQueries. It satisfies TokenUserQuerier and optionally
// APITokenLastSeenUpdater so we can assert that the best-effort
// last-seen-IP write fires (or doesn't) on each path.
type fakeTokenUserQuerier struct {
	token    sqlc.ApiToken
	user     sqlc.User
	lastSeen atomic.Int32
}

func (f *fakeTokenUserQuerier) GetTokenByHash(ctx context.Context, hash string) (sqlc.ApiToken, error) {
	return f.token, nil
}

func (f *fakeTokenUserQuerier) GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error) {
	return f.user, nil
}

func (f *fakeTokenUserQuerier) UpdateAPITokenLastUsed(ctx context.Context, id uuid.UUID) error {
	return nil
}

func (f *fakeTokenUserQuerier) UpdateAPITokenLastSeenIP(ctx context.Context, arg sqlc.UpdateAPITokenLastSeenIPParams) error {
	f.lastSeen.Add(1)
	return nil
}

func hashOf(tokenPlain string) string {
	h := sha256.Sum256([]byte(tokenPlain))
	return hex.EncodeToString(h[:])
}

func newFakeWithCIDRs(t *testing.T, cidrs string) *fakeTokenUserQuerier {
	t.Helper()
	uid := uuid.New()
	return &fakeTokenUserQuerier{
		token: sqlc.ApiToken{
			ID:           uuid.New(),
			UserID:       uid,
			TokenHash:    hashOf("astro_test_token"),
			Scopes:       json.RawMessage(`[]`),
			AllowedCidrs: cidrs,
		},
		user: sqlc.User{
			ID:       uid,
			IsActive: true,
			Email:    "t@example.com",
			Username: "t",
		},
	}
}

func runAuthRequest(t *testing.T, q *fakeTokenUserQuerier, remoteAddr, xff string) *httptest.ResponseRecorder {
	t.Helper()
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := AuthWithQueries(nil, q)(inner)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/", nil)
	req.Header.Set("Authorization", "Bearer astro_test_token")
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	t.Logf("inner_called=%v code=%d", called, rr.Code)
	return rr
}

func TestAPITokenIPAllowlist_AllowsMatching(t *testing.T) {
	q := newFakeWithCIDRs(t, "10.0.0.0/8")
	rr := runAuthRequest(t, q, "10.5.6.7:54321", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := q.lastSeen.Load(); got != 1 {
		t.Errorf("expected last-seen-IP write, got %d", got)
	}
}

func TestAPITokenIPAllowlist_RejectsUnmatching(t *testing.T) {
	q := newFakeWithCIDRs(t, "10.0.0.0/8")
	rr := runAuthRequest(t, q, "192.0.2.10:54321", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	var body map[string]map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"]["code"] != "ip_not_allowlisted" {
		t.Errorf("error.code = %q, want ip_not_allowlisted", body["error"]["code"])
	}
	if got := q.lastSeen.Load(); got != 0 {
		t.Errorf("rejected request must NOT update last-seen-ip; got %d", got)
	}
}

func TestAPITokenIPAllowlist_EmptyMeansUnrestricted(t *testing.T) {
	q := newFakeWithCIDRs(t, "")
	rr := runAuthRequest(t, q, "203.0.113.5:54321", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no IP restriction)", rr.Code)
	}
}

func TestAPITokenIPAllowlist_HandlesIPv6(t *testing.T) {
	q := newFakeWithCIDRs(t, "2001:db8::/32")
	rr := runAuthRequest(t, q, "[2001:db8::feed]:443", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (IPv6 match)", rr.Code)
	}

	q2 := newFakeWithCIDRs(t, "2001:db8::/32")
	rr2 := runAuthRequest(t, q2, "[2001:dead::feed]:443", "")
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (IPv6 miss)", rr2.Code)
	}
}

func TestAPITokenIPAllowlist_MultiCIDR(t *testing.T) {
	q := newFakeWithCIDRs(t, "10.0.0.0/8,192.168.1.5/32")
	rr1 := runAuthRequest(t, q, "192.168.1.5:443", "")
	if rr1.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (multi-CIDR match host CIDR)", rr1.Code)
	}
	q2 := newFakeWithCIDRs(t, "10.0.0.0/8,192.168.1.5/32")
	rr2 := runAuthRequest(t, q2, "192.168.1.6:443", "")
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (multi-CIDR miss)", rr2.Code)
	}
}

func TestAPITokenIPAllowlist_PreservesAuthFailure(t *testing.T) {
	// Test that a missing auth header still 401s with the original
	// authentication_required code (not the ip_not_allowlisted code).
	q := newFakeWithCIDRs(t, "10.0.0.0/8")
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := AuthWithQueries(nil, q)(inner)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/", nil)
	// No Authorization header.
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	var body map[string]map[string]string
	_ = json.NewDecoder(rr.Body).Decode(&body)
	if body["error"]["code"] != "authentication_required" {
		t.Errorf("error.code = %q, want authentication_required", body["error"]["code"])
	}
}
