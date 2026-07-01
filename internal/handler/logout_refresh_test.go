package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

// TestLogout_InvalidatesRefreshTokenViaCutoff proves logout terminates the
// whole session, not just the access-token JTI. The refresh token's JTI is
// not carried in the logout request, so Logout must bump the per-user
// tokens_invalidated_at cutoff (InvalidateAllTokens) — otherwise a retained
// 7-day refresh token stays valid and can re-mint a session after logout.
func TestLogout_InvalidatesRefreshTokenViaCutoff(t *testing.T) {
	user := makeTestUser(t, true)
	q := newMockQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	rev := newRecordingRevocationQuerier()
	h := NewAuthHandler(q, jwtMgr)
	h.SetRevocationQuerier(rev)

	token, err := jwtMgr.GenerateAccessToken(user.ID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout/", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Forwarded-Proto", "https")
	req = setAuthUser(req, user.ID.String())
	rec := httptest.NewRecorder()

	h.Logout(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(rev.invalidated) != 1 {
		t.Fatalf("InvalidateAllTokens calls = %d, want 1 (refresh token must be cut off)", len(rev.invalidated))
	}
	if rev.invalidated[0].ID != user.ID {
		t.Fatalf("invalidated ID = %v, want %v", rev.invalidated[0].ID, user.ID)
	}
	if !rev.invalidated[0].TokensInvalidatedAt.Valid {
		t.Fatal("TokensInvalidatedAt should be valid")
	}
	if d := time.Since(rev.invalidated[0].TokensInvalidatedAt.Time); d > 5*time.Second {
		t.Fatalf("TokensInvalidatedAt = %s ago, want recent (within 5s)", d)
	}
}
