package middleware

import (
	"encoding/json"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// APITokenScopeEnforce returns middleware that gates the wrapped
// handler on the API token carrying `required` (or one of the
// "everything" sentinels: `admin`, `*`). JWT-authenticated requests
// bypass the check entirely — session RBAC is the source of truth
// for the dashboard, scope enforcement is a per-token compliance
// control for headless callers.
//
// Wiring contract:
//   - `Auth` / `AuthWithQueries` MUST run first so the token row is
//     stashed in the request context.
//   - The route's existing RBAC check still runs unchanged; this
//     middleware is a *narrowing* gate on top.
//   - Pre-044 tokens (empty `scopes`) are allowed through to preserve
//     backward compatibility — operators opt in by rotating to a
//     token with at least one scope set.
func APITokenScopeEnforce(required string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, _ := GetAuthenticatedUser(r.Context())
			// JWT sessions skip scope enforcement — see godoc.
			if user == nil || user.AuthMethod != "api_token" {
				next.ServeHTTP(w, r)
				return
			}
			tok, ok := GetAuthenticatedAPIToken(r.Context())
			if !ok || tok == nil {
				// The auth middleware ran without DB queries (tests,
				// unconfigured deployment). We can't enforce scopes
				// without the row — preserve the prior behaviour and
				// let the request through. The RBAC layer is still
				// in the chain.
				next.ServeHTTP(w, r)
				return
			}
			scopes, err := auth.ParseTokenScopes(tok.Scopes)
			if err != nil {
				// Garbled JSON in the column — fail closed; this is
				// a platform-side data integrity issue, not a normal
				// client error.
				auth.APITokenDeniedTotal.WithLabelValues(observability.MetricValues("scope")...).Inc()
				scopeDenied(w, "Invalid scope metadata on this token")
				return
			}
			if !auth.ScopeAllowsRequest(scopes, required) {
				auth.APITokenDeniedTotal.WithLabelValues(observability.MetricValues("scope")...).Inc()
				scopeDenied(w, "Token is missing the required scope: "+required)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func scopeDenied(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    "scope_denied",
			"message": msg,
		},
	})
}
