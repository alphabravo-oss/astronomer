package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// scopeEnforceHarness wires the scope middleware around a sentinel
// handler whose call we observe. Returns the recorder + a bool that
// flips to true when the inner handler ran.
func scopeEnforceHarness(t *testing.T, required string, setCtx func(r *http.Request) *http.Request) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := APITokenScopeEnforce(required)(inner)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/", nil)
	if setCtx != nil {
		req = setCtx(req)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr, called
}

func TestAPITokenScopeMiddleware_AllowsScopedRequest(t *testing.T) {
	rr, called := scopeEnforceHarness(t, auth.ScopeWriteClusters, func(r *http.Request) *http.Request {
		ctx := SetAuthenticatedUserForTest(r.Context(), &AuthenticatedUser{
			ID:         "u1",
			AuthMethod: "api_token",
		})
		ctx = SetAuthenticatedAPITokenForTest(ctx, &sqlc.ApiToken{
			Scopes: json.RawMessage(`["clusters:write","read"]`),
		})
		return r.WithContext(ctx)
	})
	if !called {
		t.Fatalf("expected inner handler to run; got status %d", rr.Code)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestAPITokenScopeMiddleware_RejectsMissingScope(t *testing.T) {
	rr, called := scopeEnforceHarness(t, auth.ScopeWriteClusters, func(r *http.Request) *http.Request {
		ctx := SetAuthenticatedUserForTest(r.Context(), &AuthenticatedUser{
			ID:         "u1",
			AuthMethod: "api_token",
		})
		ctx = SetAuthenticatedAPITokenForTest(ctx, &sqlc.ApiToken{
			Scopes: json.RawMessage(`["read"]`),
		})
		return r.WithContext(ctx)
	})
	if called {
		t.Fatal("inner handler should not run when scope is missing")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
	var body map[string]map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"]["code"] != "scope_denied" {
		t.Errorf("error.code = %q, want scope_denied", body["error"]["code"])
	}
}

func TestAPITokenScopeMiddleware_AdminScopeAllowsEverything(t *testing.T) {
	rr, called := scopeEnforceHarness(t, auth.ScopeWriteRBAC, func(r *http.Request) *http.Request {
		ctx := SetAuthenticatedUserForTest(r.Context(), &AuthenticatedUser{
			ID:         "u1",
			AuthMethod: "api_token",
		})
		ctx = SetAuthenticatedAPITokenForTest(ctx, &sqlc.ApiToken{
			Scopes: json.RawMessage(`["admin"]`),
		})
		return r.WithContext(ctx)
	})
	if !called {
		t.Fatalf("admin scope should pass for any required scope; status %d", rr.Code)
	}
}

func TestAPITokenScopeMiddleware_WildcardScopeAllowsEverything(t *testing.T) {
	rr, called := scopeEnforceHarness(t, auth.ScopeWriteRBAC, func(r *http.Request) *http.Request {
		ctx := SetAuthenticatedUserForTest(r.Context(), &AuthenticatedUser{
			ID:         "u1",
			AuthMethod: "api_token",
		})
		ctx = SetAuthenticatedAPITokenForTest(ctx, &sqlc.ApiToken{
			Scopes: json.RawMessage(`["*"]`),
		})
		return r.WithContext(ctx)
	})
	if !called {
		t.Fatalf("wildcard scope should pass; status %d", rr.Code)
	}
}

func TestAPITokenScopeMiddleware_JWTSessionBypassesScopeCheck(t *testing.T) {
	rr, called := scopeEnforceHarness(t, auth.ScopeWriteClusters, func(r *http.Request) *http.Request {
		// JWT session — no api_token in context; scope check must be a no-op.
		ctx := SetAuthenticatedUserForTest(r.Context(), &AuthenticatedUser{
			ID:         "u1",
			AuthMethod: "jwt",
		})
		return r.WithContext(ctx)
	})
	if !called {
		t.Fatalf("JWT session must bypass scope check; status %d", rr.Code)
	}
}

func TestAPITokenScopeMiddleware_EmptyScopesLegacyAllow(t *testing.T) {
	// Pre-044 tokens carry `scopes: []` — must keep working so the
	// rollout is opt-in per token.
	rr, called := scopeEnforceHarness(t, auth.ScopeWriteClusters, func(r *http.Request) *http.Request {
		ctx := SetAuthenticatedUserForTest(r.Context(), &AuthenticatedUser{
			ID:         "u1",
			AuthMethod: "api_token",
		})
		ctx = SetAuthenticatedAPITokenForTest(ctx, &sqlc.ApiToken{
			Scopes: json.RawMessage(`[]`),
		})
		return r.WithContext(ctx)
	})
	if !called {
		t.Fatalf("legacy empty-scope tokens must keep working; status %d", rr.Code)
	}
}

func TestAPITokenScopeMiddleware_NoAuthContextBypass(t *testing.T) {
	// If the auth middleware didn't run (anonymous / test setup), let
	// the request through — RBAC layer or other gates will reject it.
	rr, called := scopeEnforceHarness(t, auth.ScopeWriteClusters, nil)
	if !called {
		t.Fatalf("no-auth-context should bypass and let downstream gates respond; status %d", rr.Code)
	}
}

func TestAPITokenScopeMiddleware_NoTokenInContextBypass(t *testing.T) {
	// AuthMethod==api_token but the token row isn't in context — the
	// auth middleware ran without DB queries. Must NOT crash; behave
	// the same as the pre-044 deployment.
	rr, called := scopeEnforceHarness(t, auth.ScopeWriteClusters, func(r *http.Request) *http.Request {
		ctx := SetAuthenticatedUserForTest(r.Context(), &AuthenticatedUser{
			ID:         "u1",
			AuthMethod: "api_token",
		})
		return r.WithContext(ctx)
	})
	if !called {
		t.Fatalf("missing token row must not block the request; status %d", rr.Code)
	}
}
