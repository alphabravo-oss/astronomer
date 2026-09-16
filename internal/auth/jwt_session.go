package auth

import (
	"context"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// PreparedTokenPair contains unsigned session metadata. A caller may persist
// its access JTI and refresh expiry inside a transaction, then sign after commit.
type PreparedTokenPair struct {
	accessID      string
	refreshID     string
	issuedAt      time.Time
	accessExpiry  time.Time
	refreshExpiry time.Time
}

func (p PreparedTokenPair) AccessID() string         { return p.accessID }
func (p PreparedTokenPair) RefreshExpiry() time.Time { return p.refreshExpiry }

func (m *JWTManager) PrepareTokenPairContext(ctx context.Context) (PreparedTokenPair, error) {
	if m == nil || m.KeyCount() == 0 {
		return PreparedTokenPair{}, errors.New("session signing is unavailable")
	}
	now := time.Now().UTC().Truncate(time.Second)
	return PreparedTokenPair{
		accessID: uuid.NewString(), refreshID: uuid.NewString(), issuedAt: now,
		accessExpiry:  now.Add(m.effectiveAccessTTL(ctx)).Truncate(time.Second),
		refreshExpiry: now.Add(m.refreshTokenLifetime).Truncate(time.Second),
	}, nil
}

// SignPreparedTokenPair must be called after the session transaction commits.
func (m *JWTManager) SignPreparedTokenPair(userID uuid.UUID, p PreparedTokenPair) (string, string, error) {
	if m == nil || m.KeyCount() == 0 || userID == uuid.Nil || p.accessID == "" ||
		p.refreshID == "" || !time.Now().Before(p.accessExpiry) || !time.Now().Before(p.refreshExpiry) {
		return "", "", errors.New("session metadata is invalid or expired")
	}
	sign := func(id string, kind TokenType, expiry time.Time) (string, error) {
		claims := Claims{UserID: userID, TokenType: kind, RegisteredClaims: jwt.RegisteredClaims{
			ID: id, IssuedAt: jwt.NewNumericDate(p.issuedAt), ExpiresAt: jwt.NewNumericDate(expiry),
		}}
		return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secretKeys[0])
	}
	access, err := sign(p.accessID, AccessToken, p.accessExpiry)
	if err != nil {
		return "", "", err
	}
	refresh, err := sign(p.refreshID, RefreshToken, p.refreshExpiry)
	if err != nil {
		return "", "", err
	}
	return access, refresh, nil
}
