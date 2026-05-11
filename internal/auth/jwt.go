package auth

import (
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TokenType distinguishes access from refresh tokens
type TokenType string

const (
	AccessToken  TokenType = "access"
	RefreshToken TokenType = "refresh"
)

// Claims represents the JWT claims for Astronomer tokens
type Claims struct {
	jwt.RegisteredClaims
	UserID    uuid.UUID `json:"user_id"`
	TokenType TokenType `json:"token_type"`
}

// JWTManager handles JWT token generation and validation. It supports
// multi-key rotation: the primary key signs new tokens; all configured
// keys can validate existing tokens. The single-string form keeps
// existing callers working unchanged.
type JWTManager struct {
	secretKeys           [][]byte // primary first
	accessTokenLifetime  time.Duration
	refreshTokenLifetime time.Duration
}

// NewJWTManager creates a new JWT manager. secretKey is a comma-separated
// list of HMAC signing keys; the first is the primary (used to sign new
// tokens) and any additional entries are tried only on validation. This
// makes safe online key rotation possible (see docs/secret-rotation-runbook.md):
//
//  1. add the new key as primary (secretKey="<new>,<old>")
//  2. restart — new tokens are signed under <new>; existing tokens still
//     validate because <old> remains in the validator list
//  3. wait out the longest token lifetime (refresh = 7d) so every active
//     session has been re-issued under <new>
//  4. drop the old key from config on the next restart
func NewJWTManager(secretKey string, accessLifetimeMinutes int) *JWTManager {
	if accessLifetimeMinutes <= 0 {
		accessLifetimeMinutes = 60 // default 60 min
	}
	var keys [][]byte
	for _, raw := range strings.Split(secretKey, ",") {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		keys = append(keys, []byte(s))
	}
	if len(keys) == 0 {
		// Preserve the legacy zero-key behavior; callers that hand in an
		// empty string used to get a manager with []byte("") which signed
		// and validated tokens but produced trivially-forgeable JWTs in
		// dev. We keep the same shape so existing tests don't regress.
		keys = [][]byte{[]byte(secretKey)}
	}
	return &JWTManager{
		secretKeys:           keys,
		accessTokenLifetime:  time.Duration(accessLifetimeMinutes) * time.Minute,
		refreshTokenLifetime: 7 * 24 * time.Hour, // 7 days
	}
}

// SecretKey returns a defensive copy of the PRIMARY HMAC signing key so
// other auth helpers can bind short-lived browser state to the same
// application secret without sharing mutable backing storage. Callers
// that need to validate a token signed under a non-primary key should
// use ValidateToken instead of pinning the bytes directly.
func (m *JWTManager) SecretKey() []byte {
	if m == nil || len(m.secretKeys) == 0 || len(m.secretKeys[0]) == 0 {
		return nil
	}
	out := make([]byte, len(m.secretKeys[0]))
	copy(out, m.secretKeys[0])
	return out
}

// KeyCount reports how many JWT signing keys are loaded. Useful for
// /api/v1/admin diagnostics to confirm a rotation is mid-flight (>1) vs
// steady state (==1).
func (m *JWTManager) KeyCount() int {
	if m == nil {
		return 0
	}
	return len(m.secretKeys)
}

// GenerateTokenPair creates both access and refresh tokens for a user
func (m *JWTManager) GenerateTokenPair(userID uuid.UUID) (accessToken, refreshToken string, err error) {
	accessToken, err = m.GenerateAccessToken(userID)
	if err != nil {
		return "", "", fmt.Errorf("generating access token: %w", err)
	}

	refreshToken, err = m.GenerateRefreshToken(userID)
	if err != nil {
		return "", "", fmt.Errorf("generating refresh token: %w", err)
	}

	return accessToken, refreshToken, nil
}

// GenerateAccessToken creates an access token
func (m *JWTManager) GenerateAccessToken(userID uuid.UUID) (string, error) {
	return m.generateToken(userID, AccessToken, m.accessTokenLifetime)
}

// GenerateRefreshToken creates a refresh token
func (m *JWTManager) GenerateRefreshToken(userID uuid.UUID) (string, error) {
	return m.generateToken(userID, RefreshToken, m.refreshTokenLifetime)
}

// ValidateToken parses and validates a JWT token, returning the claims.
// When multiple keys are configured (rotation in flight), each is tried in
// order; the first that yields a valid signature wins.
func (m *JWTManager) ValidateToken(tokenString string) (*Claims, error) {
	var lastErr error
	for _, key := range m.secretKeys {
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return key, nil
		})
		if err != nil {
			lastErr = err
			continue
		}
		if !token.Valid {
			lastErr = fmt.Errorf("invalid token")
			continue
		}
		if claims.UserID == uuid.Nil {
			return nil, fmt.Errorf("invalid token: missing user_id claim")
		}
		if claims.TokenType == "" {
			return nil, fmt.Errorf("invalid token: missing token_type claim")
		}
		return claims, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("invalid token")
	}
	return nil, fmt.Errorf("invalid token: %w", lastErr)
}

// generateToken is the internal token generation helper
func (m *JWTManager) generateToken(userID uuid.UUID, tokenType TokenType, lifetime time.Duration) (string, error) {
	now := time.Now()

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(lifetime)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
		UserID:    userID,
		TokenType: tokenType,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(m.secretKeys[0])
	if err != nil {
		return "", fmt.Errorf("signing token: %w", err)
	}

	return signedToken, nil
}
