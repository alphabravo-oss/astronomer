package middleware

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// AuthenticatedUserUUID returns the current authenticated user's ID as a
// pgtype.UUID for DB writers. It returns an invalid UUID when the request is
// unauthenticated or the context contains malformed user data.
func AuthenticatedUserUUID(ctx context.Context) pgtype.UUID {
	user, ok := GetAuthenticatedUser(ctx)
	if !ok || user == nil {
		return pgtype.UUID{}
	}
	id, err := uuid.Parse(user.ID)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
