package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

// parseClusterID enforces the explicit cluster route scope and writes the
// standard error envelope on failure. Callers must stop when it returns false.
func parseClusterID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	return parseClusterIDParam(w, r, "cluster_id")
}

// parseClusterIDParam is the one required cluster-route identifier parser.
// Most routes use {cluster_id}; route groups that name the same semantic
// identifier {id} pass that parameter name explicitly. All failures share the
// same public error contract and never reach persistence or member clusters.
func parseClusterIDParam(w http.ResponseWriter, r *http.Request, param string) (uuid.UUID, bool) {
	return parseScopeID(w, r, param, "cluster")
}

// parseOptionalClusterID handles collection routes where cluster_id may be a
// route parameter or a query filter. present=false means fleet-wide; ok=false
// means the caller supplied a malformed or ambiguous filter and an error was
// written. Body-derived IDs intentionally do not flow through this helper.
func parseOptionalClusterID(w http.ResponseWriter, r *http.Request) (id uuid.UUID, present, ok bool) {
	rawRoute := chi.URLParam(r, "cluster_id")
	queryValues, hasQuery := r.URL.Query()["cluster_id"]
	if rawRoute == "" && !hasQuery {
		return uuid.Nil, false, true
	}
	if hasQuery && len(queryValues) != 1 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return uuid.Nil, true, false
	}
	raw := rawRoute
	if raw == "" {
		raw = queryValues[0]
	}
	parsed, err := uuid.Parse(raw)
	if err == nil && parsed != uuid.Nil {
		if hasQuery {
			queryID, queryErr := uuid.Parse(queryValues[0])
			if queryErr != nil || queryID == uuid.Nil || queryID != parsed {
				RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
				return uuid.Nil, true, false
			}
		}
		return parsed, true, true
	}
	RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
	return uuid.Nil, true, false
}

func parseProjectID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	return parseScopeID(w, r, "project_id", "project")
}

func parseScopeID(w http.ResponseWriter, r *http.Request, param, label string) (uuid.UUID, bool) {
	id, err := reqctx.RouteUUID(r, param)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid "+label+" ID")
		return uuid.Nil, false
	}
	return id, true
}
