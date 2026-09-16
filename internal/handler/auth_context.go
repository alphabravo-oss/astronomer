package handler

import (
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/jackc/pgx/v5/pgtype"
)

func currentUserUUID(r *http.Request) pgtype.UUID {
	return reqctx.UserUUID(r.Context())
}
