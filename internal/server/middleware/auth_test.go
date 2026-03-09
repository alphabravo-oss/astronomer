package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const testSecret = "test-secret-key-for-auth-middleware"

func newTestJWTManager() *auth.JWTManager {
	return auth.NewJWTManager(testSecret, 60)
}

// generateExpiredToken creates a JWT that is already expired.
func generateExpiredToken(t *testing.T, secret string, userID uuid.UUID) string {
	t.Helper()
	now := time.Now()
	claims := auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			ID:        uuid.New().String(),
		},
		UserID:    userID,
		TokenType: auth.AccessToken,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to sign expired token: %v", err)
	}
	return signed
}

func TestAuth(t *testing.T) {
	jwtMgr := newTestJWTManager()
	userID := uuid.New()
	validToken, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("failed to generate test token: %v", err)
	}
	expiredToken := generateExpiredToken(t, testSecret, userID)

	// A simple handler that records whether it was called and the auth user.
	type handlerResult struct {
		called bool
		user   *AuthenticatedUser
	}

	tests := []struct {
		name           string
		authHeader     string
		wantStatus     int
		wantCalled     bool
		wantAuthMethod string
		wantUserID     string
		wantErrorCode  string
	}{
		{
			name:          "no authorization header",
			authHeader:    "",
			wantStatus:    http.StatusUnauthorized,
			wantCalled:    false,
			wantErrorCode: "authentication_required",
		},
		{
			name:          "invalid format - no Bearer prefix",
			authHeader:    "Basic dXNlcjpwYXNz",
			wantStatus:    http.StatusUnauthorized,
			wantCalled:    false,
			wantErrorCode: "authentication_required",
		},
		{
			name:          "invalid format - Bearer with no token",
			authHeader:    "Bearer ",
			wantStatus:    http.StatusUnauthorized,
			wantCalled:    false,
			wantErrorCode: "authentication_required",
		},
		{
			name:           "valid JWT",
			authHeader:     "Bearer " + validToken,
			wantStatus:     http.StatusOK,
			wantCalled:     true,
			wantAuthMethod: "jwt",
			wantUserID:     userID.String(),
		},
		{
			name:          "expired JWT",
			authHeader:    "Bearer " + expiredToken,
			wantStatus:    http.StatusUnauthorized,
			wantCalled:    false,
			wantErrorCode: "authentication_required",
		},
		{
			name:          "invalid JWT - garbage",
			authHeader:    "Bearer not.a.valid.jwt.token",
			wantStatus:    http.StatusUnauthorized,
			wantCalled:    false,
			wantErrorCode: "authentication_required",
		},
		{
			name:          "invalid JWT - wrong signing key",
			authHeader:    "Bearer " + mustSignWithDifferentKey(t, userID),
			wantStatus:    http.StatusUnauthorized,
			wantCalled:    false,
			wantErrorCode: "authentication_required",
		},
		{
			name:           "API token - astro_ prefix",
			authHeader:     "Bearer astro_abc123def456",
			wantStatus:     http.StatusOK,
			wantCalled:     true,
			wantAuthMethod: "api_token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result handlerResult

			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				result.called = true
				result.user, _ = GetAuthenticatedUser(r.Context())
				w.WriteHeader(http.StatusOK)
			})

			handler := Auth(jwtMgr)(inner)

			req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}

			if result.called != tt.wantCalled {
				t.Errorf("handler called = %v, want %v", result.called, tt.wantCalled)
			}

			if tt.wantErrorCode != "" {
				var body map[string]map[string]string
				if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
					t.Fatalf("failed to decode error response: %v", err)
				}
				if got := body["error"]["code"]; got != tt.wantErrorCode {
					t.Errorf("error code = %q, want %q", got, tt.wantErrorCode)
				}
			}

			if tt.wantCalled && result.user != nil {
				if result.user.AuthMethod != tt.wantAuthMethod {
					t.Errorf("auth method = %q, want %q", result.user.AuthMethod, tt.wantAuthMethod)
				}
				if tt.wantUserID != "" && result.user.ID != tt.wantUserID {
					t.Errorf("user ID = %q, want %q", result.user.ID, tt.wantUserID)
				}
			}
		})
	}
}

func TestGetAuthenticatedUser_NoValue(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	user, ok := GetAuthenticatedUser(req.Context())
	if ok || user != nil {
		t.Fatal("expected no authenticated user in empty context")
	}
}

func TestRequireAuth_SameAsAuth(t *testing.T) {
	jwtMgr := newTestJWTManager()
	// RequireAuth should behave identically to Auth - verify it rejects unauthenticated requests.
	handler := RequireAuth(jwtMgr)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("RequireAuth without header: status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

// mustSignWithDifferentKey creates a valid-looking JWT signed with a different secret.
func mustSignWithDifferentKey(t *testing.T, userID uuid.UUID) string {
	t.Helper()
	mgr := auth.NewJWTManager("completely-different-secret-key", 60)
	tok, err := mgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("failed to generate token with different key: %v", err)
	}
	return tok
}
