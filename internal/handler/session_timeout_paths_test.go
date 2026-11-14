package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/sessionpolicy"
)

const explicitSessionTTL = 10 * time.Minute

func newExplicitSessionJWT() *auth.JWTManager {
	manager := auth.MustNewJWTManager("session-timeout-path-test-secret", sessionpolicy.DefaultMinutes)
	manager.SetAccessTokenTTLProvider(func(ctx context.Context) time.Duration {
		if minutes, ok := sessionpolicy.MinutesFromContext(ctx); ok {
			return time.Duration(minutes) * time.Minute
		}
		return explicitSessionTTL
	})
	return manager
}

func requireSessionCookieTTL(t *testing.T, manager *auth.JWTManager, response *http.Response) {
	t.Helper()
	var access string
	for _, cookie := range response.Cookies() {
		if cookie.Name == auth.SessionCookieName {
			access = cookie.Value
			break
		}
	}
	if access == "" {
		t.Fatalf("response did not set %s", auth.SessionCookieName)
	}
	claims, err := manager.ValidateToken(access)
	if err != nil {
		t.Fatalf("ValidateToken(session cookie): %v", err)
	}
	if got := claims.ExpiresAt.Sub(claims.IssuedAt.Time); got != explicitSessionTTL {
		t.Fatalf("access token absolute TTL = %s, want %s", got, explicitSessionTTL)
	}
}

func TestSessionTimeoutExplicitValueAcrossActualMintPaths(t *testing.T) {
	t.Run("password", func(t *testing.T) {
		manager := newExplicitSessionJWT()
		user := makeTestUser(t, true)
		users := newMockQuerier(user)
		h := NewAuthHandler(users, manager)
		wireAuthTestMutationTx(h, &authTestMutationTx{users: users})
		h.SetSessionTimeoutPolicy(func(context.Context) int { return 10 })
		body, _ := json.Marshal(LoginRequest{Email: user.Email, Password: "testpassword"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", bytes.NewReader(body))
		w := httptest.NewRecorder()

		h.Login(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Login status = %d; body=%s", w.Code, w.Body.String())
		}
		requireSessionCookieTTL(t, manager, w.Result())
	})

	t.Run("refresh", func(t *testing.T) {
		manager := newExplicitSessionJWT()
		user := makeTestUser(t, true)
		users := newMockQuerier(user)
		h := NewAuthHandler(users, manager)
		wireAuthTestMutationTx(h, &authTestMutationTx{users: users})
		h.SetSessionTimeoutPolicy(func(context.Context) int { return 10 })
		refresh, err := manager.GenerateRefreshToken(user.ID)
		if err != nil {
			t.Fatalf("GenerateRefreshToken: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh/", nil)
		req.AddCookie(&http.Cookie{Name: auth.RefreshCookieName, Value: refresh})
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "csrf-token"})
		req.Header.Set("X-CSRF-Token", "csrf-token")
		w := httptest.NewRecorder()

		h.Refresh(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Refresh status = %d; body=%s", w.Code, w.Body.String())
		}
		requireSessionCookieTTL(t, manager, w.Result())
	})

	t.Run("totp completion", func(t *testing.T) {
		manager := newExplicitSessionJWT()
		user := makeTestUser(t, true)
		store := newFakeTOTPStore()
		encryptor := mustEncryptor(t)
		secret, _, err := auth.GenerateSecret(user.Username, "Astronomer")
		if err != nil {
			t.Fatalf("GenerateSecret: %v", err)
		}
		encrypted, err := encryptor.EncryptString(secret)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		_, _ = store.UpsertUserTOTPEnrollment(context.Background(), sqlc.UpsertUserTOTPEnrollmentParams{
			UserID:      user.ID,
			SecretKeyID: encrypted.KeyID, SecretAlgorithm: encrypted.Algorithm, SecretCiphertext: encrypted.Ciphertext, SecretNonce: encrypted.Nonce,
			ConfirmedAt: time.Now(),
		})
		codes, hashes, err := auth.GenerateRecoveryCodes(1)
		if err != nil {
			t.Fatalf("GenerateRecoveryCodes: %v", err)
		}
		_ = store.InsertRecoveryCode(context.Background(), sqlc.InsertRecoveryCodeParams{UserID: user.ID, CodeHash: hashes[0]})
		h := newTestTOTPHandler(store, newMockQuerier(user), encryptor, manager)
		challenge, err := manager.GeneratePurposeToken(user.ID, auth.PurposeTOTPChallenge, auth.TOTPChallengeTTL)
		if err != nil {
			t.Fatalf("GeneratePurposeToken: %v", err)
		}
		body, _ := json.Marshal(map[string]any{"challenge_token": challenge, "code": codes[0], "use_recovery": true})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/verify/", bytes.NewReader(body))
		w := httptest.NewRecorder()

		h.Verify(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Verify status = %d; body=%s", w.Code, w.Body.String())
		}
		requireSessionCookieTTL(t, manager, w.Result())
	})
}
