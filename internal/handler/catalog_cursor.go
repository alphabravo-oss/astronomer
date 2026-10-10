package handler

import (
	"context"
	"fmt"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
)

type catalogChartCursorQuerier interface {
	ListFilteredHelmChartsAfter(context.Context, sqlc.ListFilteredHelmChartsAfterParams) ([]sqlc.HelmChart, error)
}

func (h *CatalogHandler) SetCursorCodec(codec *paging.CursorCodec) { h.cursorCodec = codec }

// ListCharts scopes and filters before either the bounded offset or signed
// indexed continuation query. Cursor pages do not repeat a full-table COUNT.
func (h *CatalogHandler) ListCharts(w http.ResponseWriter, r *http.Request) {
	if _, ok := readCursorQuery(w, r); !ok {
		return
	}
	if !validatePageOffset(w, r) {
		return
	}
	if h.tryProjectChartCursor(w, r) {
		return
	}
	filter, ok := h.catalogChartFilter(w, r)
	if !ok {
		return
	}
	store, capable := h.queries.(catalogChartCursorQuerier)
	binding := paging.Binding("catalog-charts:v2", "name:asc,repository_id:asc,id:asc", cursorActor(r), fmt.Sprint(filter.GlobalScope), paging.SortedUUIDs(filter.RepositoryIds), filter.Tag, filter.SearchPattern, r.URL.Query().Get("project_id"), r.URL.Query().Get("cluster_id"))
	page, ok := readCursorRequest(w, r, 20, h.cursorCodec, capable, binding)
	if !ok {
		return
	}
	if page.provided && (page.cursor.Version != 2 || page.cursor.Text == nil || page.cursor.Group == nil) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid catalog chart cursor position")
		return
	}
	if !filter.GlobalScope && len(filter.RepositoryIds) == 0 {
		zero := int64(0)
		metadata, ok := page.metadata(w, r, 0, nil, &zero)
		if ok {
			paging.Write(w, []sqlc.HelmChart{}, metadata)
		}
		return
	}
	var charts []sqlc.HelmChart
	var err error
	if page.provided {
		charts, err = store.ListFilteredHelmChartsAfter(r.Context(), sqlc.ListFilteredHelmChartsAfterParams{GlobalScope: filter.GlobalScope, RepositoryIds: filter.RepositoryIds, Tag: filter.Tag, SearchPattern: filter.SearchPattern, AfterName: *page.cursor.Text, AfterRepositoryID: *page.cursor.Group, AfterID: page.cursor.ID, QueryLimit: page.queryLimit()})
	} else {
		charts, err = h.queries.ListFilteredHelmCharts(r.Context(), sqlc.ListFilteredHelmChartsParams{GlobalScope: filter.GlobalScope, RepositoryIds: filter.RepositoryIds, Tag: filter.Tag, SearchPattern: filter.SearchPattern, QueryLimit: page.queryLimit(), QueryOffset: page.offset})
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list charts")
		return
	}
	var total *int64
	if !page.mode {
		count, err := h.queries.CountFilteredHelmCharts(r.Context(), filter)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count charts")
			return
		}
		total = &count
	}
	writeCatalogChartPage(w, r, charts, page, total)
}

func writeCatalogChartPage(w http.ResponseWriter, r *http.Request, charts []sqlc.HelmChart, page cursorRequest, total *int64) {
	charts, more := trimCursorRows(charts, page)
	var next *paging.Cursor
	if more {
		last := charts[len(charts)-1]
		position := paging.NewTextCursor(last.Name, last.ID, &last.RepositoryID, page.binding, page.seen+len(charts))
		next = &position
	}
	metadata, ok := page.metadata(w, r, len(charts), next, total)
	if ok {
		paging.Write(w, charts, metadata)
	}
}
