package middleware

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestAuthenticatedUserUUID(t *testing.T) {
	userID := uuid.New()
	ctx := context.WithValue(context.Background(), userContextKey, &AuthenticatedUser{ID: userID.String()})
	got := AuthenticatedUserUUID(ctx)
	if !got.Valid {
		t.Fatal("expected valid UUID")
	}
	if got.Bytes != userID {
		t.Fatalf("UUID bytes = %s, want %s", uuid.UUID(got.Bytes), userID)
	}

	if got := AuthenticatedUserUUID(context.Background()); got.Valid {
		t.Fatalf("missing user should return invalid UUID: %+v", got)
	}

	ctx = context.WithValue(context.Background(), userContextKey, &AuthenticatedUser{ID: "not-a-uuid"})
	if got := AuthenticatedUserUUID(ctx); got.Valid {
		t.Fatalf("malformed user ID should return invalid UUID: %+v", got)
	}
}
