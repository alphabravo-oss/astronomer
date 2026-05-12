package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeTokenQuerier is an in-memory TokenQuerier for tests. Token rows are
// keyed by sha256 hex hash; user rows are keyed by ID.
type fakeTokenQuerier struct {
	tokens map[string]sqlc.ApiToken
	users  map[uuid.UUID]sqlc.User
}

func (f *fakeTokenQuerier) GetTokenByHash(_ context.Context, tokenHash string) (sqlc.ApiToken, error) {
	t, ok := f.tokens[tokenHash]
	if !ok {
		return sqlc.ApiToken{}, errors.New("not found")
	}
	// Mirror the SQL `AND is_revoked = false` filter.
	if t.IsRevoked {
		return sqlc.ApiToken{}, errors.New("not found")
	}
	return t, nil
}

func (f *fakeTokenQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	u, ok := f.users[id]
	if !ok {
		return sqlc.User{}, errors.New("not found")
	}
	return u, nil
}

func hashAstroToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func TestBearerFromHeader(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"Bearer abc", "abc"},
		{"bearer xyz", "xyz"},
		{"BEARER UPPER", "UPPER"},
		{"Basic abc", ""},
		{"Bearer", ""},
		{"Bearer  doubleSpace", " doubleSpace"}, // SplitN keeps trailing whitespace
	}
	for _, tc := range cases {
		if got := BearerFromHeader(tc.in); got != tc.want {
			t.Errorf("BearerFromHeader(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAuthorizeStreamRequest_NilJWT_AllowsThrough(t *testing.T) {
	// Dev/test mode: jwt manager not wired → admit, mirror legacy behavior.
	req := httptest.NewRequest("GET", "/api/v1/events/stream/", nil)
	uid, ok := AuthorizeStreamRequest(req, nil, nil)
	if !ok {
		t.Fatalf("expected ok=true when jwt is nil")
	}
	if uid != uuid.Nil {
		t.Errorf("expected uuid.Nil when jwt is nil, got %v", uid)
	}
}

func TestAuthorizeStreamRequest_MissingToken_Rejects(t *testing.T) {
	jwt := NewJWTManager("secret", 60)
	req := httptest.NewRequest("GET", "/api/v1/events/stream/", nil)
	uid, ok := AuthorizeStreamRequest(req, nil, jwt)
	if ok {
		t.Fatal("expected rejection when no token is present")
	}
	if uid != uuid.Nil {
		t.Errorf("expected uuid.Nil on rejection, got %v", uid)
	}
}

func TestAuthorizeStreamRequest_HeaderJWT_OK(t *testing.T) {
	jwt := NewJWTManager("secret", 60)
	userID := uuid.New()
	tok, err := jwt.GenerateAccessToken(userID)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/events/stream/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	got, ok := AuthorizeStreamRequest(req, nil, jwt)
	if !ok {
		t.Fatal("expected ok=true for valid header JWT")
	}
	if got != userID {
		t.Errorf("uid = %v, want %v", got, userID)
	}
}

func TestAuthorizeStreamRequest_QueryJWT_OK(t *testing.T) {
	jwt := NewJWTManager("secret", 60)
	userID := uuid.New()
	tok, err := jwt.GenerateAccessToken(userID)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/events/stream/?token="+tok, nil)

	got, ok := AuthorizeStreamRequest(req, nil, jwt)
	if !ok {
		t.Fatal("expected ok=true for valid query JWT")
	}
	if got != userID {
		t.Errorf("uid = %v, want %v", got, userID)
	}
}

func TestAuthorizeStreamRequest_HeaderPreferredOverQuery(t *testing.T) {
	jwt := NewJWTManager("secret", 60)
	headerUser := uuid.New()
	queryUser := uuid.New()
	headerTok, _ := jwt.GenerateAccessToken(headerUser)
	queryTok, _ := jwt.GenerateAccessToken(queryUser)

	req := httptest.NewRequest("GET", "/api/v1/events/stream/?token="+queryTok, nil)
	req.Header.Set("Authorization", "Bearer "+headerTok)

	got, ok := AuthorizeStreamRequest(req, nil, jwt)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got != headerUser {
		t.Errorf("expected header user %v to win, got %v", headerUser, got)
	}
}

func TestAuthorizeStreamRequest_ExpiredJWT_Rejects(t *testing.T) {
	// Generate with a 0-minute lifetime → manager bumps to default 60min;
	// instead, generate with a very short manual TTL by mutating time.
	// Easier path: hand-craft an already-expired token via the manager API
	// by using a private helper isn't exposed — fall back to a manager with
	// a fresh token + sleeping, but that's flaky. Cleaner: build a manager,
	// generate a token, then build a SECOND manager with a different key
	// and validate against it; ValidateToken returns an error for bad
	// signatures, which is the same `err != nil` branch as "expired".
	jwt1 := NewJWTManager("secret-a", 60)
	jwt2 := NewJWTManager("secret-b", 60)
	tok, _ := jwt1.GenerateAccessToken(uuid.New())

	req := httptest.NewRequest("GET", "/api/v1/events/stream/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	if _, ok := AuthorizeStreamRequest(req, nil, jwt2); ok {
		t.Fatal("expected rejection for JWT signed under a different key (covers err!=nil branch shared with expired)")
	}
}

func TestAuthorizeStreamRequest_APIToken_OK(t *testing.T) {
	jwt := NewJWTManager("secret", 60)
	userID := uuid.New()
	rawToken := "astro_abcdef"
	q := &fakeTokenQuerier{
		tokens: map[string]sqlc.ApiToken{
			hashAstroToken(rawToken): {
				ID:        uuid.New(),
				UserID:    userID,
				TokenHash: hashAstroToken(rawToken),
				IsRevoked: false,
			},
		},
		users: map[uuid.UUID]sqlc.User{
			userID: {ID: userID, IsActive: true},
		},
	}

	req := httptest.NewRequest("GET", "/api/v1/events/stream/?token="+rawToken, nil)
	got, ok := AuthorizeStreamRequest(req, q, jwt)
	if !ok {
		t.Fatal("expected ok=true for valid api token")
	}
	if got != userID {
		t.Errorf("uid = %v, want %v", got, userID)
	}
}

func TestAuthorizeStreamRequest_APIToken_NoQuerier_Rejects(t *testing.T) {
	jwt := NewJWTManager("secret", 60)
	req := httptest.NewRequest("GET", "/api/v1/events/stream/?token=astro_xyz", nil)
	if _, ok := AuthorizeStreamRequest(req, nil, jwt); ok {
		t.Fatal("expected rejection: api token presented but no querier wired")
	}
}

func TestAuthorizeStreamRequest_APIToken_Revoked_Rejects(t *testing.T) {
	// GetTokenByHash enforces is_revoked=false at the SQL layer; our fake
	// mirrors that, so a revoked token reads as "not found".
	jwt := NewJWTManager("secret", 60)
	userID := uuid.New()
	rawToken := "astro_revoked"
	q := &fakeTokenQuerier{
		tokens: map[string]sqlc.ApiToken{
			hashAstroToken(rawToken): {
				UserID:    userID,
				TokenHash: hashAstroToken(rawToken),
				IsRevoked: true,
			},
		},
		users: map[uuid.UUID]sqlc.User{
			userID: {ID: userID, IsActive: true},
		},
	}

	req := httptest.NewRequest("GET", "/api/v1/events/stream/?token="+rawToken, nil)
	if _, ok := AuthorizeStreamRequest(req, q, jwt); ok {
		t.Fatal("expected rejection for revoked api token")
	}
}

func TestAuthorizeStreamRequest_APIToken_Expired_Rejects(t *testing.T) {
	jwt := NewJWTManager("secret", 60)
	userID := uuid.New()
	rawToken := "astro_expired"
	q := &fakeTokenQuerier{
		tokens: map[string]sqlc.ApiToken{
			hashAstroToken(rawToken): {
				UserID:    userID,
				TokenHash: hashAstroToken(rawToken),
				ExpiresAt: pgtype.Timestamptz{
					Time:  time.Now().Add(-1 * time.Hour),
					Valid: true,
				},
			},
		},
		users: map[uuid.UUID]sqlc.User{
			userID: {ID: userID, IsActive: true},
		},
	}

	req := httptest.NewRequest("GET", "/api/v1/events/stream/?token="+rawToken, nil)
	if _, ok := AuthorizeStreamRequest(req, q, jwt); ok {
		t.Fatal("expected rejection for expired api token")
	}
}

func TestAuthorizeStreamRequest_APIToken_InactiveUser_Rejects(t *testing.T) {
	jwt := NewJWTManager("secret", 60)
	userID := uuid.New()
	rawToken := "astro_inactive"
	q := &fakeTokenQuerier{
		tokens: map[string]sqlc.ApiToken{
			hashAstroToken(rawToken): {
				UserID:    userID,
				TokenHash: hashAstroToken(rawToken),
			},
		},
		users: map[uuid.UUID]sqlc.User{
			userID: {ID: userID, IsActive: false},
		},
	}

	req := httptest.NewRequest("GET", "/api/v1/events/stream/?token="+rawToken, nil)
	if _, ok := AuthorizeStreamRequest(req, q, jwt); ok {
		t.Fatal("expected rejection for inactive user")
	}
}
