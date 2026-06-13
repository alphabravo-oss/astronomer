package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestAuthBrowserOrBearer covers the three branches of the dual-mode auth
// middleware: bearer header (XHR), session cookie (browser nav), and
// failure with redirect-vs-401 selection.
func TestAuthBrowserOrBearer(t *testing.T) {
	jwtMgr := newTestJWTManager()
	userID := uuid.New()
	validToken, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	mw := AuthBrowserOrBearer(jwtMgr, nil, "/auth/login")
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, ok := GetAuthenticatedUser(r.Context()); !ok || u == nil {
			t.Errorf("expected user in context")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	t.Run("bearer header passes through", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/argocd/applications", nil)
		req.Header.Set("Authorization", "Bearer "+validToken)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("got %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("cookie is promoted to bearer", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/argocd/applications", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: validToken})
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("got %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("cookie post requires csrf", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/argocd/api/applications", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: validToken})
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401; body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("cookie post with csrf passes", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/argocd/api/applications", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: validToken})
		req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "csrf-token"})
		req.Header.Set("X-CSRF-Token", "csrf-token")
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("got %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("bearer post does not require csrf", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/argocd/api/applications", nil)
		req.Header.Set("Authorization", "Bearer "+validToken)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("got %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("missing creds on browser nav redirects to login", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/argocd/applications", nil)
		req.Header.Set("Accept", "text/html")
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		if rr.Code != http.StatusFound {
			t.Errorf("got %d, want 302; body=%s", rr.Code, rr.Body.String())
		}
		loc := rr.Header().Get("Location")
		if !strings.HasPrefix(loc, "/auth/login?returnTo=") {
			t.Errorf("Location=%q, want /auth/login?returnTo=...", loc)
		}
	})

	t.Run("missing creds on XHR returns JSON 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/argocd/applications", nil)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401; body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("websocket upgrade with no creds returns 401, not redirect", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/argocd/api/stream", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Accept", "text/html")
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401 for ws upgrade without creds", rr.Code)
		}
	})
}
