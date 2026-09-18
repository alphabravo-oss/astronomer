package reqctx

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// RouteUUID parses an explicitly named route parameter. It never takes scope
// from a query string, body, or an unrelated object's generic {id} parameter.
func RouteUUID(r *http.Request, name string) (uuid.UUID, error) {
	if r == nil {
		return uuid.Nil, fmt.Errorf("missing route parameter %s", name)
	}
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid route parameter %s: %w", name, err)
	}
	if id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("route parameter %s must not be nil", name)
	}
	return id, nil
}

// ClusterID resolves the explicit cluster scope of a route.
func ClusterID(r *http.Request) (uuid.UUID, error) {
	return RouteUUID(r, "cluster_id")
}

// ProjectID resolves the explicit project scope of a route.
func ProjectID(r *http.Request) (uuid.UUID, error) {
	return RouteUUID(r, "project_id")
}
