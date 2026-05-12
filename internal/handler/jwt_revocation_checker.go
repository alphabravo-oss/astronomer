package handler

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// JWTRevocationCheckerBackend is the narrow query surface the
// JWTRevocationChecker needs from the database. Defined locally so the
// auth package — which only consumes the auth.RevocationChecker
// interface — doesn't have to import sqlc.
type JWTRevocationCheckerBackend interface {
	IsJWTRevoked(ctx context.Context, jti string) (bool, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

// jwtRevocationChecker bridges sqlc.Queries to the auth.RevocationChecker
// contract. It answers two questions:
//
//   - is THIS JTI on the deny list? (Logout / per-token revoke)
//   - what's the per-user "invalidate everything before this timestamp"
//     cutoff? (admin force-logout)
//
// Both are cheap reads — the per-JTI check is a covered PK lookup, the
// per-user cutoff is a single-column read on the user row. The
// validator caches positive verdicts so even these light queries are
// only paid once per JTI per cache TTL.
type jwtRevocationChecker struct {
	backend JWTRevocationCheckerBackend
}

// NewJWTRevocationChecker constructs an auth.RevocationChecker that
// resolves against the DB. The returned value is safe to pass to
// JWTManager.SetRevocationChecker.
func NewJWTRevocationChecker(b JWTRevocationCheckerBackend) auth.RevocationChecker {
	if b == nil {
		return nil
	}
	return &jwtRevocationChecker{backend: b}
}

func (c *jwtRevocationChecker) IsJWTRevoked(ctx context.Context, jti string) (bool, error) {
	return c.backend.IsJWTRevoked(ctx, jti)
}

func (c *jwtRevocationChecker) UserTokensInvalidatedAt(ctx context.Context, userID uuid.UUID) (time.Time, bool, error) {
	u, err := c.backend.GetUserByID(ctx, userID)
	if err != nil {
		return time.Time{}, false, err
	}
	if !u.TokensInvalidatedAt.Valid {
		return time.Time{}, false, nil
	}
	return u.TokensInvalidatedAt.Time, true, nil
}
