package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

// authContextKey is an unexported type for auth-related context keys.
type authContextKey string

const (
	userContextKey authContextKey = "authenticated_user"
)

// AuthenticatedUser represents the user extracted from auth.
type AuthenticatedUser struct {
	ID       string
	Email    string
	Username string
	// AuthMethod indicates how the user was authenticated ("jwt" or "api_token").
	AuthMethod string
}

// GetAuthenticatedUser extracts the authenticated user from context.
func GetAuthenticatedUser(ctx context.Context) (*AuthenticatedUser, bool) {
	u, ok := ctx.Value(userContextKey).(*AuthenticatedUser)
	return u, ok
}

// authError writes a JSON 401 error response.
func authError(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	resp := map[string]interface{}{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	}
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// Auth creates middleware that authenticates requests via JWT or API token.
//
// Check order:
//  1. Authorization: Bearer astro_* -> API token (SHA-256 hash lookup)
//  2. Authorization: Bearer <jwt>   -> JWT validation
//  3. No auth -> 401
func Auth(jwtManager *auth.JWTManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if header == "" {
				authError(w, "authentication_required", "Authorization header is required")
				return
			}

			parts := strings.SplitN(header, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				authError(w, "authentication_required", "Authorization header must use Bearer scheme")
				return
			}

			token := parts[1]
			if token == "" {
				authError(w, "authentication_required", "Bearer token is empty")
				return
			}

			var user *AuthenticatedUser

			if strings.HasPrefix(token, "astro_") {
				// API token path: hash the token and create a placeholder user.
				// The actual DB lookup will be wired in later.
				hash := sha256.Sum256([]byte(token))
				tokenHash := hex.EncodeToString(hash[:])

				user = &AuthenticatedUser{
					ID:         "api_token:" + tokenHash[:12],
					AuthMethod: "api_token",
				}
			} else {
				// JWT path
				claims, err := jwtManager.ValidateToken(token)
				if err != nil {
					authError(w, "authentication_required", "Invalid or expired token")
					return
				}

				user = &AuthenticatedUser{
					ID:         claims.UserID.String(),
					AuthMethod: "jwt",
				}
			}

			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAuth is an alias for Auth that makes it explicit at the call site
// that authentication is mandatory for the wrapped routes.
func RequireAuth(jwtManager *auth.JWTManager) func(http.Handler) http.Handler {
	return Auth(jwtManager)
}
