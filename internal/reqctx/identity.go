// Package reqctx defines transport request identity and scope primitives.
// It is a leaf package: it must not depend on application domains or adapters.
package reqctx

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type identityKey struct{}

// User is the identity established by authentication middleware.
type User struct {
	ID                 string
	Email              string
	Username           string
	AuthMethod         string
	FirstName          string
	LastName           string
	IsActive           bool
	IsStaff            bool
	IsSuperuser        bool
	MustChangePassword bool
	DateJoined         time.Time
	LastLogin          time.Time
	HasLastLogin       bool
	// Resolved is true only when authentication loaded the authoritative user
	// row (or its coordinated short-lived cache entry). Hand-built contexts in
	// tests and internal callers remain unresolved and handlers may explicitly
	// fall back to their own store lookup when they need the full profile.
	Resolved bool
}

// WithUser attaches an authenticated identity to a request context.
func WithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, identityKey{}, user)
}

// AuthenticatedUser returns the identity, or false for an unauthenticated context.
func AuthenticatedUser(ctx context.Context) (*User, bool) {
	user, ok := ctx.Value(identityKey{}).(*User)
	return user, ok && user != nil
}

// UserUUID returns an invalid database UUID for missing or malformed identities.
func UserUUID(ctx context.Context) pgtype.UUID {
	user, ok := AuthenticatedUser(ctx)
	if !ok {
		return pgtype.UUID{}
	}
	id, err := uuid.Parse(user.ID)
	if err != nil || id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
