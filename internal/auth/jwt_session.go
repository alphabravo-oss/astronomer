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
	familyID      uuid.UUID
	issuedAt      time.Time
	accessExpiry  time.Time
	refreshExpiry time.Time
}

func (p PreparedTokenPair) AccessID() string         { return p.accessID }
func (p PreparedTokenPair) RefreshID() string        { return p.refreshID }
func (p PreparedTokenPair) FamilyID() uuid.UUID      { return p.familyID }
func (p PreparedTokenPair) IssuedAt() time.Time      { return p.issuedAt }
func (p PreparedTokenPair) RefreshExpiry() time.Time { return p.refreshExpiry }

func (m *JWTManager) PrepareTokenPairContext(ctx context.Context) (PreparedTokenPair, error) {
	if m == nil || m.KeyCount() == 0 {
		return PreparedTokenPair{}, errors.New("session signing is unavailable")
	}
	now := time.Now().UTC().Truncate(time.Second)
	return PreparedTokenPair{
		accessID: uuid.NewString(), refreshID: uuid.NewString(), familyID: uuid.New(), issuedAt: now,
		accessExpiry:  now.Add(m.effectiveAccessTTL(ctx)).Truncate(time.Second),
		refreshExpiry: now.Add(m.refreshTokenLifetime).Truncate(time.Second),
	}, nil
}

// PrepareRotationContext reserves fresh JTIs inside an existing browser
// session family. Rotation preserves the family's absolute refresh expiry.
func (m *JWTManager) PrepareRotationContext(ctx context.Context, familyID uuid.UUID, familyExpiry time.Time) (PreparedTokenPair, error) {
	pair, err := m.PrepareTokenPairContext(ctx)
	if err != nil {
		return PreparedTokenPair{}, err
	}
	if familyID == uuid.Nil || !time.Now().Before(familyExpiry) {
		return PreparedTokenPair{}, errors.New("refresh session family is invalid or expired")
	}
	pair.familyID = familyID
	if familyExpiry.Before(pair.refreshExpiry) {
		pair.refreshExpiry = familyExpiry.UTC().Truncate(time.Second)
	}
	return pair, nil
}

// SignPreparedTokenPair must be called after the session transaction commits.
func (m *JWTManager) SignPreparedTokenPair(userID uuid.UUID, p PreparedTokenPair) (string, string, error) {
	if m == nil || m.KeyCount() == 0 || userID == uuid.Nil || p.accessID == "" ||
		p.refreshID == "" || p.familyID == uuid.Nil || !time.Now().Before(p.accessExpiry) || !time.Now().Before(p.refreshExpiry) {
		return "", "", errors.New("session metadata is invalid or expired")
	}
	sign := func(id string, kind TokenType, expiry time.Time) (string, error) {
		claims := Claims{UserID: userID, TokenType: kind, SessionFamilyID: p.familyID, BrowserSession: true,
			RegisteredClaims: m.registeredClaims(userID, id, p.issuedAt, expiry)}
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
