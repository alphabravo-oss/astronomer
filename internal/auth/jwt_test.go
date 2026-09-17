package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/sessionpolicy"
)

func TestJWTManager(t *testing.T) {
	secretKey := "test-secret-key-for-jwt-testing"
	userID := uuid.New()

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "generate access token and validate returns correct claims",
			run: func(t *testing.T) {
				mgr := MustNewJWTManager(secretKey, 60)

				token, err := mgr.GenerateAccessToken(userID)
				if err != nil {
					t.Fatalf("GenerateAccessToken() error = %v", err)
				}
				if token == "" {
					t.Fatal("GenerateAccessToken() returned empty token")
				}

				claims, err := mgr.ValidateToken(token)
				if err != nil {
					t.Fatalf("ValidateToken() error = %v", err)
				}

				if claims.UserID != userID {
					t.Errorf("UserID = %v, want %v", claims.UserID, userID)
				}
				if claims.TokenType != AccessToken {
					t.Errorf("TokenType = %v, want %v", claims.TokenType, AccessToken)
				}
				if claims.ID == "" {
					t.Error("JTI should not be empty")
				}
				if claims.IssuedAt == nil {
					t.Error("IssuedAt should not be nil")
				}
				if claims.ExpiresAt == nil {
					t.Error("ExpiresAt should not be nil")
				}
			},
		},
		{
			name: "generate refresh token and validate returns correct token_type",
			run: func(t *testing.T) {
				mgr := MustNewJWTManager(secretKey, 60)

				token, err := mgr.GenerateRefreshToken(userID)
				if err != nil {
					t.Fatalf("GenerateRefreshToken() error = %v", err)
				}

				claims, err := mgr.ValidateToken(token)
				if err != nil {
					t.Fatalf("ValidateToken() error = %v", err)
				}

				if claims.TokenType != RefreshToken {
					t.Errorf("TokenType = %v, want %v", claims.TokenType, RefreshToken)
				}
				if claims.UserID != userID {
					t.Errorf("UserID = %v, want %v", claims.UserID, userID)
				}
			},
		},
		{
			name: "generate token pair returns two valid tokens",
			run: func(t *testing.T) {
				mgr := MustNewJWTManager(secretKey, 60)

				accessToken, refreshToken, err := mgr.GenerateTokenPair(userID)
				if err != nil {
					t.Fatalf("GenerateTokenPair() error = %v", err)
				}

				accessClaims, err := mgr.ValidateToken(accessToken)
				if err != nil {
					t.Fatalf("ValidateToken(access) error = %v", err)
				}
				if accessClaims.TokenType != AccessToken {
					t.Errorf("access TokenType = %v, want %v", accessClaims.TokenType, AccessToken)
				}

				refreshClaims, err := mgr.ValidateToken(refreshToken)
				if err != nil {
					t.Fatalf("ValidateToken(refresh) error = %v", err)
				}
				if refreshClaims.TokenType != RefreshToken {
					t.Errorf("refresh TokenType = %v, want %v", refreshClaims.TokenType, RefreshToken)
				}

				// Each token should have a unique JTI
				if accessClaims.ID == refreshClaims.ID {
					t.Error("access and refresh tokens should have different JTIs")
				}
			},
		},
		{
			name: "expired token fails validation",
			run: func(t *testing.T) {
				mgr := MustNewJWTManager(secretKey, 60)

				// Create a token that expired in the past
				now := time.Now()
				claims := Claims{
					RegisteredClaims: jwt.RegisteredClaims{
						ExpiresAt: jwt.NewNumericDate(now.Add(-1 * time.Hour)),
						IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
						ID:        uuid.New().String(),
					},
					UserID:    userID,
					TokenType: AccessToken,
				}
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
				signedToken, err := token.SignedString([]byte(secretKey))
				if err != nil {
					t.Fatalf("signing token: %v", err)
				}

				_, err = mgr.ValidateToken(signedToken)
				if err == nil {
					t.Fatal("ValidateToken() should fail for expired token")
				}
				t.Logf("Expected error for expired token: %v", err)
			},
		},
		{
			name: "wrong secret key fails validation",
			run: func(t *testing.T) {
				mgr := MustNewJWTManager(secretKey, 60)
				wrongMgr := MustNewJWTManager("wrong-secret-key", 60)

				token, err := mgr.GenerateAccessToken(userID)
				if err != nil {
					t.Fatalf("GenerateAccessToken() error = %v", err)
				}

				_, err = wrongMgr.ValidateToken(token)
				if err == nil {
					t.Fatal("ValidateToken() should fail with wrong secret key")
				}
				t.Logf("Expected error for wrong key: %v", err)
			},
		},
		{
			name: "malformed token fails validation",
			run: func(t *testing.T) {
				mgr := MustNewJWTManager(secretKey, 60)

				_, err := mgr.ValidateToken("not.a.valid.jwt.token")
				if err == nil {
					t.Fatal("ValidateToken() should fail for malformed token")
				}
				t.Logf("Expected error for malformed token: %v", err)

				_, err = mgr.ValidateToken("")
				if err == nil {
					t.Fatal("ValidateToken() should fail for empty token")
				}
				t.Logf("Expected error for empty token: %v", err)

				_, err = mgr.ValidateToken("garbage")
				if err == nil {
					t.Fatal("ValidateToken() should fail for garbage input")
				}
				t.Logf("Expected error for garbage token: %v", err)
			},
		},
		{
			// dev-keys-default-and-silent: this used to return a manager that
			// signed with a zero-length HMAC key, i.e. tokens anyone could mint.
			name: "empty secret key is rejected",
			run: func(t *testing.T) {
				for _, key := range []string{"", "   ", ",", " , "} {
					mgr, err := NewJWTManager(key, 0)
					if err == nil {
						t.Fatalf("NewJWTManager(%q) = %v, want error", key, mgr)
					}
					if mgr != nil {
						t.Fatalf("NewJWTManager(%q) returned a manager alongside the error", key)
					}
				}
			},
		},
		{
			name: "default access lifetime is 15 minutes when zero provided",
			run: func(t *testing.T) {
				mgr := MustNewJWTManager(secretKey, 0)

				token, err := mgr.GenerateAccessToken(userID)
				if err != nil {
					t.Fatalf("GenerateAccessToken() error = %v", err)
				}

				claims, err := mgr.ValidateToken(token)
				if err != nil {
					t.Fatalf("ValidateToken() error = %v", err)
				}

				// Expiry should be approximately 15 minutes from now.
				expectedExpiry := time.Now().Add(15 * time.Minute)
				diff := claims.ExpiresAt.Sub(expectedExpiry)
				if diff < -5*time.Second || diff > 5*time.Second {
					t.Errorf("expiry %v not within 5s of expected %v", claims.ExpiresAt.Time, expectedExpiry)
				}
			},
		},
		{
			name: "negative access lifetime uses default",
			run: func(t *testing.T) {
				mgr := MustNewJWTManager(secretKey, -10)

				token, err := mgr.GenerateAccessToken(userID)
				if err != nil {
					t.Fatalf("GenerateAccessToken() error = %v", err)
				}

				claims, err := mgr.ValidateToken(token)
				if err != nil {
					t.Fatalf("ValidateToken() error = %v", err)
				}

				expectedExpiry := time.Now().Add(15 * time.Minute)
				diff := claims.ExpiresAt.Sub(expectedExpiry)
				if diff < -5*time.Second || diff > 5*time.Second {
					t.Errorf("expiry %v not within 5s of expected %v", claims.ExpiresAt.Time, expectedExpiry)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

// AUTH-R02: session.timeout_minutes provider is applied at access-token mint
// without requiring each caller to SetAccessTokenTTL first.
func TestAccessTokenTTLProviderAppliedAtMint(t *testing.T) {
	mgr := MustNewJWTManager("ttl-provider-secret", 15)
	mgr.SetAccessTokenTTLProvider(func(context.Context) time.Duration {
		return 15 * time.Minute
	})
	userID := uuid.New()
	token, err := mgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("GenerateAccessToken: %v", err)
	}
	claims, err := mgr.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	expected := time.Now().Add(15 * time.Minute)
	diff := claims.ExpiresAt.Sub(expected)
	if diff < -5*time.Second || diff > 5*time.Second {
		t.Errorf("expiry %v not within 5s of provider TTL %v (diff %v)", claims.ExpiresAt.Time, expected, diff)
	}
	// Boot TTL unchanged; provider only affects mint.
	if mgr.AccessTokenTTL() != 15*time.Minute {
		t.Errorf("AccessTokenTTL() = %v, want 15m base", mgr.AccessTokenTTL())
	}
}

func TestNewJWTManagerBoundsBootSessionTimeout(t *testing.T) {
	for _, minutes := range []int{0, -1, sessionpolicy.MinMinutes - 1, sessionpolicy.MaxMinutes + 1} {
		mgr := MustNewJWTManager("test-secret", minutes)
		if got := mgr.AccessTokenTTL(); got != sessionpolicy.DefaultMinutes*time.Minute {
			t.Errorf("MustNewJWTManager(_, %d) TTL = %s, want %s", minutes, got, sessionpolicy.DefaultMinutes*time.Minute)
		}
	}
}

func TestValidateTokenRejectsDifferentHMACAlgorithm(t *testing.T) {
	const secret = "exact-algorithm-test-secret"
	mgr := MustNewJWTManager(secret, sessionpolicy.DefaultMinutes)
	token, err := mgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := jwt.NewParser().ParseUnverified(token, &Claims{})
	if err != nil {
		t.Fatal(err)
	}
	forged := jwt.NewWithClaims(jwt.SigningMethodHS384, parsed.Claims)
	wrongAlgorithmToken, err := forged.SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.ValidateToken(wrongAlgorithmToken); err == nil || !strings.Contains(err.Error(), "signing method") {
		t.Fatalf("ValidateToken(HS384) error = %v, want exact-algorithm rejection", err)
	}
}

func TestJWTManagerEnforcesTrustAndSessionContext(t *testing.T) {
	const secret = "strict-context-test-secret"
	mgr, err := NewJWTManagerWithConfig(JWTConfig{
		SecretKey: secret, AccessLifetimeMinutes: 15,
		Issuer: "https://control.example.test", Audience: "astronomer-console",
	})
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.New()
	access, refresh, err := mgr.GenerateTokenPair(userID)
	if err != nil {
		t.Fatal(err)
	}
	accessClaims, err := mgr.ValidateToken(access)
	if err != nil {
		t.Fatal(err)
	}
	refreshClaims, err := mgr.ValidateToken(refresh)
	if err != nil {
		t.Fatal(err)
	}
	if accessClaims.Issuer != "https://control.example.test" || accessClaims.Subject != userID.String() ||
		len(accessClaims.Audience) != 1 || accessClaims.Audience[0] != "astronomer-console" || accessClaims.ID == "" {
		t.Fatalf("access trust context is incomplete: %#v", accessClaims.RegisteredClaims)
	}
	if accessClaims.SessionFamilyID == uuid.Nil || accessClaims.SessionFamilyID != refreshClaims.SessionFamilyID {
		t.Fatalf("token pair family mismatch: %s / %s", accessClaims.SessionFamilyID, refreshClaims.SessionFamilyID)
	}

	sign := func(claims Claims) string {
		t.Helper()
		raw, signErr := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
		if signErr != nil {
			t.Fatal(signErr)
		}
		return raw
	}
	tests := map[string]func(*Claims){
		"wrong issuer":     func(c *Claims) { c.Issuer = "other" },
		"wrong audience":   func(c *Claims) { c.Audience = jwt.ClaimStrings{"other"} },
		"subject mismatch": func(c *Claims) { c.Subject = uuid.NewString() },
		"missing jti":      func(c *Claims) { c.ID = "" },
		"missing family":   func(c *Claims) { c.SessionFamilyID = uuid.Nil },
		"unsupported type": func(c *Claims) { c.TokenType = TokenType("admin") },
		"session purpose":  func(c *Claims) { c.Purpose = "totp_challenge" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			claims := *accessClaims
			mutate(&claims)
			if _, validateErr := mgr.ValidateToken(sign(claims)); validateErr == nil {
				t.Fatal("validation accepted malformed trust/session context")
			}
		})
	}
}

func TestNewJWTManagerWithConfigRejectsEmptyTrustContext(t *testing.T) {
	for _, cfg := range []JWTConfig{
		{SecretKey: "secret", Audience: "audience"},
		{SecretKey: "secret", Issuer: "issuer"},
	} {
		if _, err := NewJWTManagerWithConfig(cfg); err == nil {
			t.Fatalf("NewJWTManagerWithConfig(%+v) succeeded", cfg)
		}
	}
}

// Multi-key rotation: tokens signed under the old key must still validate
// after a new key is promoted to primary; new tokens must sign under the
// new primary. This mirrors the rotation procedure in
// docs/secret-rotation-runbook.md.
func TestJWTManagerMultiKeyRotation(t *testing.T) {
	oldKey := "jwt-old-test-secret-1234567890ab"
	newKey := "jwt-new-test-secret-fedcba098765"
	userID := uuid.New()

	mgrOld := MustNewJWTManager(oldKey, 60)
	if mgrOld.KeyCount() != 1 {
		t.Errorf("KeyCount = %d, want 1", mgrOld.KeyCount())
	}
	tokenUnderOld, err := mgrOld.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("GenerateAccessToken(old): %v", err)
	}

	// Rotation in flight: new primary, old fallback.
	mgrMixed := MustNewJWTManager(newKey+","+oldKey, 60)
	if mgrMixed.KeyCount() != 2 {
		t.Errorf("KeyCount = %d, want 2", mgrMixed.KeyCount())
	}

	// Old-signed token still validates.
	claims, err := mgrMixed.ValidateToken(tokenUnderOld)
	if err != nil {
		t.Fatalf("ValidateToken(old): %v", err)
	}
	if claims.UserID != userID {
		t.Errorf("UserID = %v, want %v", claims.UserID, userID)
	}

	// New tokens are signed under the new primary — they validate
	// against the new-only manager (the steady-state post-rotation).
	tokenUnderNew, err := mgrMixed.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("GenerateAccessToken(mixed): %v", err)
	}
	mgrNewOnly := MustNewJWTManager(newKey, 60)
	if _, err := mgrNewOnly.ValidateToken(tokenUnderNew); err != nil {
		t.Fatalf("ValidateToken(new token under new-only): %v", err)
	}
	// Old token must NOT validate once the old key is dropped — that's the
	// "wait out token lifetime before dropping" gate.
	if _, err := mgrNewOnly.ValidateToken(tokenUnderOld); err == nil {
		t.Error("expected old-keyed token to FAIL under new-only manager")
	}
}
