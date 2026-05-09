package handler

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func currentUserUUID(r *http.Request) pgtype.UUID {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		return pgtype.UUID{}
	}
	id, err := uuid.Parse(user.ID)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
