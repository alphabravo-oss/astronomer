package handler

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// catalogChartFilter resolves authorization once; the exact same predicate is
// used by both the SQL page and count. Search never filters an already-cut page.
func (h *CatalogHandler) catalogChartFilter(w http.ResponseWriter, r *http.Request) (sqlc.CountFilteredHelmChartsParams, bool) {
	filter, ok := catalogChartSearchFilter(w, r)
	if !ok {
		return filter, false
	}
	projectID, projectScoped, ok := catalogProjectQuery(w, r)
	if !ok {
		return filter, false
	}
	clusterID, clusterScoped, ok := catalogClusterQuery(w, r)
	if !ok {
		return filter, false
	}
	if filter.Tag != "" && (projectScoped || clusterScoped) {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidRequest, "tag filtering is available only on the global catalog")
		return filter, false
	}
	if projectScoped {
		if !h.authz.authorizeProjectAction(w, r, projectID, rbac.ResourceCatalog, rbac.VerbRead) {
			return filter, false
		}
		catalogs, err := h.queries.ListCatalogsForProject(r.Context(), projectID)
		if err != nil {
			apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.ListError, "Failed to resolve project catalogs")
			return filter, false
		}
		for _, catalog := range catalogs {
			filter.RepositoryIds = append(filter.RepositoryIds, catalog.ID)
		}
		return filter, true
	}
	if clusterScoped {
		ids, err := h.visibleCatalogRepositoryIDs(r.Context(), clusterID)
		if err != nil {
			if errors.Is(err, errCatalogClusterAccessDenied) {
				apiutil.WriteError(w, r, http.StatusForbidden, apiutil.Forbidden, "You do not have catalog access on this cluster")
			} else {
				apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.ListError, "Failed to resolve cluster catalogs")
			}
			return filter, false
		}
		filter.RepositoryIds = ids
		return filter, true
	}
	if !h.authz.authorizeGlobalAction(w, r, rbac.ResourceCatalog, rbac.VerbRead) {
		return filter, false
	}
	filter.GlobalScope = true
	return filter, true
}

func catalogChartSearchFilter(w http.ResponseWriter, r *http.Request) (sqlc.CountFilteredHelmChartsParams, bool) {
	filter := sqlc.CountFilteredHelmChartsParams{Tag: strings.TrimSpace(r.URL.Query().Get("tag"))}
	search := r.URL.Query().Get("search")
	if !utf8.ValidString(search) || utf8.RuneCountInString(search) > 256 || len(r.URL.Query()["search"]) > 1 {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidRequest, "search must be one string of at most 256 characters")
		return filter, false
	}
	filter.SearchPattern = literalChartSearchPattern(strings.TrimSpace(search))
	return filter, true
}

func literalChartSearchPattern(search string) string {
	if search == "" {
		return ""
	}
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search) + "%"
}
