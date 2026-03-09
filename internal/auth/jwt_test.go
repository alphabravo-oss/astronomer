package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestJWTManager(t *testing.T) {
	secretKey := "test-secret-key-for-jwt-testing"
	userID := uuid.New()

	tests := []struct {
		name    string
		run     func(t *testing.T)
	}{
		{
			name: "generate access token and validate returns correct claims",
			run: func(t *testing.T) {
				mgr := NewJWTManager(secretKey, 60)

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
				mgr := NewJWTManager(secretKey, 60)

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
				mgr := NewJWTManager(secretKey, 60)

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
				mgr := NewJWTManager(secretKey, 60)

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
				mgr := NewJWTManager(secretKey, 60)
				wrongMgr := NewJWTManager("wrong-secret-key", 60)

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
				mgr := NewJWTManager(secretKey, 60)

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
			name: "empty secret key still creates a manager with defaults",
			run: func(t *testing.T) {
				mgr := NewJWTManager("", 0)

				// Should use default 60 min lifetime
				token, err := mgr.GenerateAccessToken(userID)
				if err != nil {
					t.Fatalf("GenerateAccessToken() with empty key error = %v", err)
				}

				// Should still validate with the same (empty) key
				claims, err := mgr.ValidateToken(token)
				if err != nil {
					t.Fatalf("ValidateToken() error = %v", err)
				}
				if claims.UserID != userID {
					t.Errorf("UserID = %v, want %v", claims.UserID, userID)
				}
			},
		},
		{
			name: "default access lifetime is 60 minutes when zero provided",
			run: func(t *testing.T) {
				mgr := NewJWTManager(secretKey, 0)

				token, err := mgr.GenerateAccessToken(userID)
				if err != nil {
					t.Fatalf("GenerateAccessToken() error = %v", err)
				}

				claims, err := mgr.ValidateToken(token)
				if err != nil {
					t.Fatalf("ValidateToken() error = %v", err)
				}

				// Expiry should be approximately 60 minutes from now
				expectedExpiry := time.Now().Add(60 * time.Minute)
				diff := claims.ExpiresAt.Time.Sub(expectedExpiry)
				if diff < -5*time.Second || diff > 5*time.Second {
					t.Errorf("expiry %v not within 5s of expected %v", claims.ExpiresAt.Time, expectedExpiry)
				}
			},
		},
		{
			name: "negative access lifetime uses default",
			run: func(t *testing.T) {
				mgr := NewJWTManager(secretKey, -10)

				token, err := mgr.GenerateAccessToken(userID)
				if err != nil {
					t.Fatalf("GenerateAccessToken() error = %v", err)
				}

				claims, err := mgr.ValidateToken(token)
				if err != nil {
					t.Fatalf("ValidateToken() error = %v", err)
				}

				expectedExpiry := time.Now().Add(60 * time.Minute)
				diff := claims.ExpiresAt.Time.Sub(expectedExpiry)
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
