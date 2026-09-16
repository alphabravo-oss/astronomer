package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// authContextKey is an unexported type for auth-related context keys.
// APITokenLastSeenUpdater is the optional capability used by the IP-
// allowlist enforcer to stamp the last-seen remote IP onto the token
// row. Wired through TokenUserQuerier in production via *sqlc.Queries;
// the in-memory test fake doesn't have to implement it.
type APITokenLastSeenUpdater interface {
	UpdateAPITokenLastSeenIP(ctx context.Context, arg sqlc.UpdateAPITokenLastSeenIPParams) error
}

// authError writes a JSON 401 error response.
func authError(w http.ResponseWriter, code, message string) {
	authErrorStatus(w, http.StatusUnauthorized, code, message)
}

func authErrorStatus(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]interface{}{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
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

// AuthWithQueries authenticates requests via JWT, API token, or the browser
// HttpOnly session cookie, using DB lookups when provided. Authorization
// headers take precedence so headless API-token callers are unaffected even
// when a browser cookie is also present.
func AuthWithQueries(jwtManager *auth.JWTManager, queries auth.TokenUserQuerier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			usedSessionCookie := false
			if header == "" {
				if c, err := r.Cookie(auth.SessionCookieName); err == nil && strings.TrimSpace(c.Value) != "" {
					header = "Bearer " + c.Value
					usedSessionCookie = true
				} else {
					authError(w, "authentication_required", "Authorization header or session cookie is required")
					return
				}
			}
			if usedSessionCookie && !isSafeMethod(r.Method) && !auth.ValidateCSRF(r) {
				authError(w, "csrf_required", "CSRF token is required")
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

			var user *reqctx.User
			var apiTokenForCtx *sqlc.ApiToken

			if strings.HasPrefix(token, "astro_") {
				hash := sha256.Sum256([]byte(token))
				tokenHash := hex.EncodeToString(hash[:])
				if queries == nil {
					user = &reqctx.User{
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
						if perr != nil || !auth.IPAllowed(nets, netIP(reqctx.ClientIP(r))) {
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
						if ip := reqctx.ClientIP(r); ip != nil {
							_ = updater.UpdateAPITokenLastSeenIP(r.Context(), sqlc.UpdateAPITokenLastSeenIPParams{
								ID:               apiToken.ID,
								LastSeenRemoteIp: ip.String(),
							})
						}
					}
					user = requestUserFromDatabase(dbUser, "api_token")
					tok := apiToken
					apiTokenForCtx = &tok
				}
			} else {
				// JWT path
				claims, err := jwtManager.ValidateTokenContext(r.Context(), token)
				if err != nil {
					if errors.Is(err, auth.ErrRevocationUnavailable) {
						w.Header().Set("Retry-After", "2")
						authErrorStatus(w, http.StatusServiceUnavailable, "authentication_dependency_unavailable", "Authentication state is temporarily unavailable")
						return
					}
					authError(w, "authentication_required", "Invalid or expired token")
					return
				}
				// Purpose-bound tokens (TOTP challenge etc.) are NEVER accepted
				// as session credentials. They're valid for their dedicated
				// endpoint only — POST /auth/totp/verify reads them via
				// JWTManager.ValidateToken + Purpose check directly.
				if claims.TokenType == auth.PurposeToken {
					authError(w, "authentication_required", "Token is not valid for this endpoint")
					return
				}

				user = &reqctx.User{
					ID:         claims.UserID.String(),
					AuthMethod: "jwt",
				}
				if queries != nil {
					// Fail closed, mirroring the api-token branch above: a
					// JWT whose user was deleted (lookup error) or
					// deactivated (is_active=false) must be rejected here.
					// Previously the lookup error was ignored and IsActive
					// was never checked, so a live JWT outlived the account
					// change until its own natural expiry.
					identity, resolveErr := jwtManager.ResolveSessionIdentity(r.Context(), claims.ID, claims.UserID, func(ctx context.Context) (auth.SessionIdentity, error) {
						dbUser, lookupErr := queries.GetUserByID(ctx, claims.UserID)
						if lookupErr != nil {
							return auth.SessionIdentity{}, lookupErr
						}
						if !dbUser.IsActive {
							return auth.SessionIdentity{}, errors.New("user is inactive")
						}
						return sessionIdentityFromDatabase(dbUser), nil
					})
					if resolveErr != nil {
						authError(w, "authentication_required", "Invalid or expired token")
						return
					}
					user = requestUserFromSessionIdentity(identity, "jwt")
				}
			}

			ctx := reqctx.WithUser(r.Context(), user)
			if apiTokenForCtx != nil {
				ctx = auth.WithAuthenticatedAPIToken(ctx, apiTokenForCtx)
			}
			setRequestLogActor(ctx, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func requestUserFromDatabase(user sqlc.User, method string) *reqctx.User {
	return requestUserFromSessionIdentity(sessionIdentityFromDatabase(user), method)
}

func sessionIdentityFromDatabase(user sqlc.User) auth.SessionIdentity {
	return auth.SessionIdentity{
		UserID: user.ID, Email: user.Email, Username: user.Username,
		FirstName: user.FirstName, LastName: user.LastName,
		IsActive: user.IsActive, IsStaff: user.IsStaff, IsSuperuser: user.IsSuperuser,
		MustChangePassword: user.MustChangePassword, DateJoined: user.DateJoined,
		LastLogin: user.LastLogin.Time, HasLastLogin: user.LastLogin.Valid,
	}
}

func requestUserFromSessionIdentity(identity auth.SessionIdentity, method string) *reqctx.User {
	return &reqctx.User{
		ID: identity.UserID.String(), Email: identity.Email, Username: identity.Username,
		AuthMethod: method, FirstName: identity.FirstName, LastName: identity.LastName,
		IsActive: identity.IsActive, IsStaff: identity.IsStaff, IsSuperuser: identity.IsSuperuser,
		MustChangePassword: identity.MustChangePassword, DateJoined: identity.DateJoined,
		LastLogin: identity.LastLogin, HasLastLogin: identity.HasLastLogin, Resolved: true,
	}
}

func netIP(address *netip.Addr) net.IP {
	if address == nil {
		return nil
	}
	return net.IP(address.AsSlice())
}

// RequireAuth is an alias for Auth that makes it explicit at the call site
// that authentication is mandatory for the wrapped routes.
func RequireAuth(jwtManager *auth.JWTManager) func(http.Handler) http.Handler {
	return AuthWithQueries(jwtManager, nil)
}

// RequireAuthWithQueries is the DB-backed variant used in production.
func RequireAuthWithQueries(jwtManager *auth.JWTManager, queries auth.TokenUserQuerier) func(http.Handler) http.Handler {
	return AuthWithQueries(jwtManager, queries)
}
