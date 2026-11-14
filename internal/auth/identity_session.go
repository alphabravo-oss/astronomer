package auth

import (
	"context"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func (m *JWTManager) SetIdentityValidator(fn func(context.Context, uuid.UUID, string, string, time.Time) error) {
	m.revMu.Lock()
	defer m.revMu.Unlock()
	m.identityValidator = fn
}

// Identity sessions have no refresh credential. Reauthentication is required at
// the earlier of the product session limit and the upstream token expiry.
func (m *JWTManager) GenerateIdentitySession(ctx context.Context, userID uuid.UUID, issuer, subject string, upstreamExpiry time.Time) (string, error) {
	now := time.Now()
	expiry := now.Add(m.effectiveAccessTTL(ctx))
	if upstreamExpiry.Before(expiry) {
		expiry = upstreamExpiry
	}
	if issuer == "" || subject == "" || userID == uuid.Nil || !expiry.After(now) {
		return "", errors.New("invalid identity session bounds")
	}
	claims := Claims{RegisteredClaims: m.registeredClaims(userID, uuid.NewString(), now, expiry), UserID: userID, TokenType: AccessToken, SessionFamilyID: uuid.New(), IdentityIssuer: issuer, IdentitySubject: subject}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secretKeys[0])
}
