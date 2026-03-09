package auth

import (
	"fmt"
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

// JWTManager handles JWT token generation and validation
type JWTManager struct {
	secretKey            []byte
	accessTokenLifetime  time.Duration
	refreshTokenLifetime time.Duration
}

// NewJWTManager creates a new JWT manager
func NewJWTManager(secretKey string, accessLifetimeMinutes int) *JWTManager {
	if accessLifetimeMinutes <= 0 {
		accessLifetimeMinutes = 60 // default 60 min
	}
	return &JWTManager{
		secretKey:            []byte(secretKey),
		accessTokenLifetime:  time.Duration(accessLifetimeMinutes) * time.Minute,
		refreshTokenLifetime: 7 * 24 * time.Hour, // 7 days
	}
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

// ValidateToken parses and validates a JWT token, returning the claims
func (m *JWTManager) ValidateToken(tokenString string) (*Claims, error) {
	claims := &Claims{}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.secretKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	if claims.UserID == uuid.Nil {
		return nil, fmt.Errorf("invalid token: missing user_id claim")
	}

	if claims.TokenType == "" {
		return nil, fmt.Errorf("invalid token: missing token_type claim")
	}

	return claims, nil
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
	signedToken, err := token.SignedString(m.secretKey)
	if err != nil {
		return "", fmt.Errorf("signing token: %w", err)
	}

	return signedToken, nil
}
