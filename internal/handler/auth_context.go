package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func currentUserUUID(r *http.Request) pgtype.UUID {
	return middleware.AuthenticatedUserUUID(r.Context())
}
