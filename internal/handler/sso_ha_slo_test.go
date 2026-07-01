package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Finding #1 (HA SSO): the callback must not require the CSRF state to
// live in the local process's in-memory map. In an HA install the Login
// (state stored in replica A) and the IdP redirect can land on a
// different replica B whose map is empty. Before the fix the callback
// returned 403 "OAuth state did not match" on that miss, breaking SSO
// whenever the two requests hit different replicas. After the fix the
// callback ignores the in-memory miss and advances to the stateless,
// HA-safe signed-cookie check (here surfaced as "cookie missing"). The
// signed cookie remains the CSRF authority.
func TestSSOCallbackHASafeIgnoresInMemoryState(t *testing.T) {
	h := testSSOHandler(t)
	// Deliberately do NOT rememberState: replica B never saw the Login
	// that replica A served, so its in-memory state map is empty.
	req := withProvider(httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback/github/?code=x&state=cross-replica", nil), "github")
	rec := httptest.NewRecorder()

	h.Callback(rec, req)

	if strings.Contains(rec.Body.String(), "state did not match") {
		t.Fatalf("callback still gated on in-memory state (HA-broken): %s", rec.Body.String())
	}
	// Having passed the in-memory gate, it stops deterministically at the
	// signed-cookie check (no cookie present, no network involved).
	if !strings.Contains(rec.Body.String(), "cookie missing") {
		t.Fatalf("expected to advance to the signed-cookie check, got: %s", rec.Body.String())
	}
}

type fakeSSOSessionWriter struct {
	called bool
	params sqlc.InsertSSOSessionParams
}

func (f *fakeSSOSessionWriter) InsertSSOSession(_ context.Context, arg sqlc.InsertSSOSessionParams) error {
	f.called = true
	f.params = arg
	return nil
}

// Finding #2 (single sign-out): the sso_sessions row carries the upstream
// id_token_hint that drives RP-initiated logout. It is captured only once,
// at login — the SPA's silent access-token refresh never re-fetches it —
// so its lifetime must track the refresh-token (session) expiry, not the
// access-token expiry (minutes). If expires_at is anchored to the access
// token, the nightly purge drops the row long before the session ends and
// single sign-out is dead for any session older than one access lifetime.
func TestPersistSSOSessionExpiresAtTracksRefreshToken(t *testing.T) {
	h := testSSOHandler(t)
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	writer := &fakeSSOSessionWriter{}
	h.encryptor = enc
	h.sessionWriter = writer

	uid := uuid.New()
	access, err := h.jwt.GenerateAccessToken(uid)
	if err != nil {
		t.Fatalf("access token: %v", err)
	}
	refresh, err := h.jwt.GenerateRefreshToken(uid)
	if err != nil {
		t.Fatalf("refresh token: %v", err)
	}

	info := &auth.SSOUserInfo{
		Email:              "sso@example.com",
		Provider:           "okta",
		UpstreamIDToken:    "raw-upstream-id-token",
		EndSessionEndpoint: "https://idp.example.com/logout",
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback/okta/", nil)

	h.persistSSOSession(req, uid, "okta", access, info, refresh)

	if !writer.called {
		t.Fatal("expected an sso_sessions row to be written")
	}
	accessClaims, err := h.jwt.ValidateToken(access)
	if err != nil {
		t.Fatalf("validate access: %v", err)
	}
	refreshClaims, err := h.jwt.ValidateToken(refresh)
	if err != nil {
		t.Fatalf("validate refresh: %v", err)
	}
	if !writer.params.ExpiresAt.Equal(refreshClaims.ExpiresAt.Time) {
		t.Fatalf("ExpiresAt = %v, want refresh-token exp %v", writer.params.ExpiresAt, refreshClaims.ExpiresAt.Time)
	}
	if !writer.params.ExpiresAt.After(accessClaims.ExpiresAt.Time) {
		t.Fatalf("ExpiresAt %v must outlive the access-token exp %v, else SLO dies after one refresh", writer.params.ExpiresAt, accessClaims.ExpiresAt.Time)
	}
	// Still keyed by the access JTI for the pre-refresh direct-lookup path.
	if writer.params.Jti != accessClaims.ID {
		t.Fatalf("Jti = %q, want access JTI %q", writer.params.Jti, accessClaims.ID)
	}
}
