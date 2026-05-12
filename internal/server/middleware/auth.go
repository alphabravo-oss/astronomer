package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/google/uuid"
)

// authContextKey is an unexported type for auth-related context keys.
type authContextKey string

const (
	userContextKey     authContextKey = "authenticated_user"
	apiTokenContextKey authContextKey = "authenticated_api_token"
)

// AuthenticatedUser represents the user extracted from auth.
type AuthenticatedUser struct {
	ID       string
	Email    string
	Username string
	// AuthMethod indicates how the user was authenticated ("jwt" or "api_token").
	AuthMethod string
}

// TokenUserQuerier resolves API tokens to concrete users.
type TokenUserQuerier interface {
	GetTokenByHash(ctx context.Context, tokenHash string) (sqlc.ApiToken, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	UpdateAPITokenLastUsed(ctx context.Context, id uuid.UUID) error
}

// APITokenLastSeenUpdater is the optional capability used by the IP-
// allowlist enforcer to stamp the last-seen remote IP onto the token
// row. Wired through TokenUserQuerier in production via *sqlc.Queries;
// the in-memory test fake doesn't have to implement it.
type APITokenLastSeenUpdater interface {
	UpdateAPITokenLastSeenIP(ctx context.Context, arg sqlc.UpdateAPITokenLastSeenIPParams) error
}

// GetAuthenticatedUser extracts the authenticated user from context.
func GetAuthenticatedUser(ctx context.Context) (*AuthenticatedUser, bool) {
	u, ok := ctx.Value(userContextKey).(*AuthenticatedUser)
	return u, ok
}

// GetAuthenticatedAPIToken returns the validated API token row when the
// current request was authenticated via API token. Returns (nil, false)
// for JWT-authenticated requests (the dashboard / browser SPA). Used
// by APITokenScopeEnforce so the scope check doesn't re-query the DB.
func GetAuthenticatedAPIToken(ctx context.Context) (*sqlc.ApiToken, bool) {
	t, ok := ctx.Value(apiTokenContextKey).(*sqlc.ApiToken)
	return t, ok
}

// SetAuthenticatedAPITokenForTest injects an API token row into the
// context. Tests use this to drive APITokenScopeEnforce without
// running the full Auth middleware.
func SetAuthenticatedAPITokenForTest(ctx context.Context, tok *sqlc.ApiToken) context.Context {
	return context.WithValue(ctx, apiTokenContextKey, tok)
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
	return AuthWithQueries(jwtManager, nil)
}

// AuthWithQueries authenticates requests via JWT or API token, using DB lookups when provided.
func AuthWithQueries(jwtManager *auth.JWTManager, queries TokenUserQuerier) func(http.Handler) http.Handler {
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
			var apiTokenForCtx *sqlc.ApiToken

			if strings.HasPrefix(token, "astro_") {
				hash := sha256.Sum256([]byte(token))
				tokenHash := hex.EncodeToString(hash[:])
				if queries == nil {
					user = &AuthenticatedUser{
						ID:         "api_token:" + tokenHash[:12],
						AuthMethod: "api_token",
					}
				} else {
					apiToken, err := queries.GetTokenByHash(r.Context(), tokenHash)
					if err != nil {
						authError(w, "authentication_required", "Invalid or expired token")
						return
					}
					if apiToken.ExpiresAt.Valid && apiToken.ExpiresAt.Time.Before(time.Now()) {
						authError(w, "authentication_required", "Invalid or expired token")
						return
					}
					dbUser, err := queries.GetUserByID(r.Context(), apiToken.UserID)
					if err != nil || !dbUser.IsActive {
						authError(w, "authentication_required", "Invalid or expired token")
						return
					}
					// Migration-044: per-token IP allowlist. Empty
					// allowed_cidrs preserves the pre-044 behaviour
					// (no restriction). Parse errors fail closed so a
					// misconfigured token can't silently bypass the
					// check.
					if strings.TrimSpace(apiToken.AllowedCidrs) != "" {
						nets, perr := auth.ParseAllowedCIDRs(apiToken.AllowedCidrs)
						if perr != nil || !auth.IPAllowed(nets, auth.RemoteIPForRequest(r)) {
							auth.APITokenDeniedTotal.WithLabelValues(observability.MetricValues("ip")...).Inc()
							authError(w, "ip_not_allowlisted", "Token not permitted from this IP address")
							return
						}
					}
					_ = queries.UpdateAPITokenLastUsed(r.Context(), apiToken.ID)
					// Best-effort last-seen IP stamp — never fail the
					// request on a write error. Cast through the
					// optional capability interface so test fakes that
					// don't expose the new method still satisfy
					// TokenUserQuerier.
					if updater, ok := queries.(APITokenLastSeenUpdater); ok && updater != nil {
						if ip := auth.RemoteIPForRequest(r); ip != nil {
							_ = updater.UpdateAPITokenLastSeenIP(r.Context(), sqlc.UpdateAPITokenLastSeenIPParams{
								ID:               apiToken.ID,
								LastSeenRemoteIp: ip.String(),
							})
						}
					}
					user = &AuthenticatedUser{
						ID:         dbUser.ID.String(),
						Email:      dbUser.Email,
						Username:   dbUser.Username,
						AuthMethod: "api_token",
					}
					tok := apiToken
					apiTokenForCtx = &tok
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
				if queries != nil {
					if dbUser, err := queries.GetUserByID(r.Context(), claims.UserID); err == nil {
						user.Email = dbUser.Email
						user.Username = dbUser.Username
					}
				}
			}

			ctx := context.WithValue(r.Context(), userContextKey, user)
			if apiTokenForCtx != nil {
				ctx = context.WithValue(ctx, apiTokenContextKey, apiTokenForCtx)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAuth is an alias for Auth that makes it explicit at the call site
// that authentication is mandatory for the wrapped routes.
func RequireAuth(jwtManager *auth.JWTManager) func(http.Handler) http.Handler {
	return AuthWithQueries(jwtManager, nil)
}

// RequireAuthWithQueries is the DB-backed variant used in production.
func RequireAuthWithQueries(jwtManager *auth.JWTManager, queries TokenUserQuerier) func(http.Handler) http.Handler {
	return AuthWithQueries(jwtManager, queries)
}

// SetAuthenticatedUserForTest injects an AuthenticatedUser into the context.
// This is intended for use in tests only.
func SetAuthenticatedUserForTest(ctx context.Context, user *AuthenticatedUser) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}
