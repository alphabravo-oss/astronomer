package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// TokenQuerier is the minimal database surface AuthorizeStreamRequest needs to
// resolve an API token (the `astro_*` prefix) back to an active user. It is
// satisfied by *sqlc.Queries and by middleware.TokenUserQuerier so existing
// call sites can pass whichever they already hold.
type TokenQuerier interface {
	GetTokenByHash(ctx context.Context, tokenHash string) (sqlc.ApiToken, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

// BearerFromHeader extracts a token from an "Authorization: Bearer X" header.
// Returns the empty string when the header is absent, malformed, or uses a
// different scheme. The "Bearer" scheme match is case-insensitive (RFC 7235).
func BearerFromHeader(headerVal string) string {
	if headerVal == "" {
		return ""
	}
	parts := strings.SplitN(headerVal, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

// AuthorizeStreamRequest authenticates a long-lived stream request (WebSocket
// or SSE) by Authorization header OR `?token=` query parameter. Returns the
// authenticated user ID and true on success.
//
// The token may be either:
//   - An API token (prefix `astro_`), looked up by sha256 hash in the
//     api_tokens table. The query also enforces is_revoked=false at the SQL
//     level. ExpiresAt is checked here. The owning user must be active.
//   - A JWT, validated against the JWTManager's key set.
//
// Precedence: Authorization header is preferred over the query parameter,
// matching the existing per-handler implementations this helper replaces.
//
// On any failure (missing/expired/revoked/inactive-user/bad-signature) returns
// (uuid.Nil, false) so callers can write a uniform 401. When the JWTManager
// is nil the request is allowed through with uuid.Nil so dev/test callers
// that haven't wired auth continue to work; this mirrors the pre-existing
// per-handler `if jwt == nil { return true }` short-circuit.
func AuthorizeStreamRequest(r *http.Request, q TokenQuerier, j *JWTManager) (uuid.UUID, bool) {
	if r == nil {
		return uuid.Nil, false
	}
	if j == nil {
		// No JWT manager wired → dev/test mode. Preserve the legacy behavior
		// of admitting the request rather than 401-ing every stream.
		return uuid.Nil, true
	}

	token := BearerFromHeader(r.Header.Get("Authorization"))
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		return uuid.Nil, false
	}

	if strings.HasPrefix(token, "astro_") {
		if q == nil {
			// No DB lookup available → can't verify an api_token; reject.
			return uuid.Nil, false
		}
		sum := sha256.Sum256([]byte(token))
		hashStr := hex.EncodeToString(sum[:])
		apiToken, err := q.GetTokenByHash(r.Context(), hashStr)
		if err != nil {
			return uuid.Nil, false
		}
		if apiToken.ExpiresAt.Valid && apiToken.ExpiresAt.Time.Before(time.Now()) {
			return uuid.Nil, false
		}
		dbUser, err := q.GetUserByID(r.Context(), apiToken.UserID)
		if err != nil || !dbUser.IsActive {
			return uuid.Nil, false
		}
		return dbUser.ID, true
	}

	claims, err := j.ValidateToken(token)
	if err != nil {
		return uuid.Nil, false
	}
	if claims.UserID == uuid.Nil {
		return uuid.Nil, false
	}
	return claims.UserID, true
}
