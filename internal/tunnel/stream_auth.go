package tunnel

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

// authenticateStreamRequest is the single auth boundary for tunnel-owned
// long-lived pod streams. Browser callers should use one-use stream tickets;
// non-browser callers can still use Authorization headers through the shared
// auth.AuthorizeStreamRequestWithTickets validator.
func authenticateStreamRequest(r *http.Request, queries auth.TokenQuerier, jwt *auth.JWTManager, tickets *auth.StreamTicketStore, kind string) (uuid.UUID, bool) {
	clusterID, _ := uuid.Parse(chi.URLParam(r, "cluster_id"))
	return auth.AuthorizeStreamRequestWithTickets(r, queries, jwt, tickets, kind, clusterID)
}
