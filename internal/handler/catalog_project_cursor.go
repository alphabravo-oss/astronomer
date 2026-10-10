package handler

import (
	"context"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

type catalogProjectChartCursorQuerier interface {
	ListProjectHelmChartsPage(context.Context, sqlc.ListProjectHelmChartsPageParams) ([]sqlc.HelmChart, error)
	ListProjectHelmChartsAfter(context.Context, sqlc.ListProjectHelmChartsAfterParams) ([]sqlc.HelmChart, error)
}

// Signed project pages keep repository visibility in SQL. The legacy offset
// adapter retains its existing repository/count contract for released clients.
func (h *CatalogHandler) tryProjectChartCursor(w http.ResponseWriter, r *http.Request) bool {
	if !r.URL.Query().Has("cursor") || r.URL.Query().Get("project_id") == "" {
		return false
	}
	projectID, _, ok := catalogProjectQuery(w, r)
	if !ok {
		return true
	}
	filter, ok := catalogChartSearchFilter(w, r)
	if !ok {
		return true
	}
	if filter.Tag != "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "tag filtering is available only on the global catalog")
		return true
	}
	if !h.authz.authorizeProjectAction(w, r, projectID, rbac.ResourceCatalog, rbac.VerbRead) {
		return true
	}
	store, capable := h.queries.(catalogProjectChartCursorQuerier)
	binding := paging.Binding("catalog-project-charts:v1", "name:asc,repository_id:asc,id:asc", cursorActor(r), projectID.String(), filter.SearchPattern)
	page, ok := readCursorRequest(w, r, 20, h.cursorCodec, capable, binding)
	if !ok {
		return true
	}
	if page.provided && (page.cursor.Version != 2 || page.cursor.Text == nil || page.cursor.Group == nil) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid catalog chart cursor position")
		return true
	}
	var charts []sqlc.HelmChart
	var err error
	if page.provided {
		charts, err = store.ListProjectHelmChartsAfter(r.Context(), sqlc.ListProjectHelmChartsAfterParams{ProjectID: projectID, SearchPattern: filter.SearchPattern, AfterName: *page.cursor.Text, AfterRepositoryID: *page.cursor.Group, AfterID: page.cursor.ID, QueryLimit: page.queryLimit()})
	} else {
		charts, err = store.ListProjectHelmChartsPage(r.Context(), sqlc.ListProjectHelmChartsPageParams{ProjectID: projectID, SearchPattern: filter.SearchPattern, QueryLimit: page.queryLimit()})
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list charts")
		return true
	}
	writeCatalogChartPage(w, r, charts, page, nil)
	return true
}
