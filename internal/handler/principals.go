package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type PrincipalQueries interface {
	SearchUsers(context.Context, sqlc.SearchUsersParams) ([]sqlc.User, error)
}
type PrincipalHandler struct{ queries PrincipalQueries }

func NewPrincipalHandler(q PrincipalQueries) *PrincipalHandler {
	return &PrincipalHandler{queries: q}
}
func (h *PrincipalHandler) Search(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()["q"]
	if len(values) != 1 || len(strings.TrimSpace(values[0])) < 3 || len(values[0]) > 128 {
		apiutil.WriteError(w, r, 400, apiutil.ValidationError, "Search requires 3 to 128 characters")
		return
	}
	if h == nil || h.queries == nil {
		apiutil.WriteError(w, r, 503, apiutil.NotConfigured, "User directory is unavailable")
		return
	}
	users, err := h.queries.SearchUsers(r.Context(), sqlc.SearchUsersParams{Search: strings.TrimSpace(values[0]), PageLimit: 50})
	if err != nil {
		apiutil.WriteError(w, r, 503, apiutil.LoadError, "User directory is unavailable")
		return
	}
	items := make([]map[string]string, 0, len(users))
	for _, u := range users {
		if u.IsActive {
			items = append(items, map[string]string{"kind": "local", "user_id": u.ID.String(), "email": u.Email, "username": u.Username, "display_name": strings.TrimSpace(u.FirstName + " " + u.LastName)})
		}
	}
	RespondJSON(w, 200, map[string]any{"principals": items, "connectors": []any{}})
}
