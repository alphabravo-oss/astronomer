package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
)

// This is an initial measurement cap, not a claim that a plan passes the
// unchanged 250 examined-row budget. Canonical generated rows are pending SQLC.
const catalogGlobalCandidateWindow int32 = 16

type catalogGlobalWindowQuerier interface {
	ListCatalogGlobalCandidateWindowFirst(context.Context, sqlc.ListCatalogGlobalCandidateWindowFirstParams) ([]sqlc.ListCatalogGlobalCandidateWindowFirstRow, error)
	ListCatalogGlobalCandidateWindowAfter(context.Context, sqlc.ListCatalogGlobalCandidateWindowAfterParams) ([]sqlc.ListCatalogGlobalCandidateWindowAfterRow, error)
}

type catalogGlobalCandidate struct {
	chart   sqlc.HelmChart
	matches bool
}

func catalogGlobalWindowEligible(filter sqlc.CountFilteredHelmChartsParams) bool {
	// A mixed global/private repository union continues through its scoped
	// existing producer; global eligibility is never inferred from auth role.
	return filter.GlobalScope && len(filter.RepositoryIds) == 0
}

func catalogGlobalWindowSize(requested int32) int32 {
	return min(requested, catalogGlobalCandidateWindow)
}

func listCatalogGlobalWindow(ctx context.Context, q catalogGlobalWindowQuerier, filter sqlc.CountFilteredHelmChartsParams, page cursorRequest) ([]catalogGlobalCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit := catalogGlobalWindowSize(page.limit) + 1
	out := []catalogGlobalCandidate{}
	if !page.provided {
		rows, err := q.ListCatalogGlobalCandidateWindowFirst(ctx, sqlc.ListCatalogGlobalCandidateWindowFirstParams{Tag: filter.Tag, SearchPattern: filter.SearchPattern, CandidateLimit: limit})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, catalogGlobalCandidate{chart: sqlc.HelmChart{
				ID:           row.ID,
				RepositoryID: row.RepositoryID,
				Name:         row.Name,
				DisplayName:  row.DisplayName,
				Description:  row.Description,
				IconUrl:      row.IconUrl,
				HomeUrl:      row.HomeUrl,
				Category:     row.Category,
				Keywords:     row.Keywords,
				Maintainers:  row.Maintainers,
				Deprecated:   row.Deprecated,
				CreatedAt:    row.CreatedAt,
				UpdatedAt:    row.UpdatedAt,
			}, matches: row.Matches})
		}
		return out, nil
	}
	if page.cursor.Text == nil || page.cursor.Group == nil {
		return nil, errors.New("catalog candidate position is incomplete")
	}
	rows, err := q.ListCatalogGlobalCandidateWindowAfter(ctx, sqlc.ListCatalogGlobalCandidateWindowAfterParams{Tag: filter.Tag, SearchPattern: filter.SearchPattern, AfterName: *page.cursor.Text, AfterRepositoryID: *page.cursor.Group, AfterID: page.cursor.ID, CandidateLimit: limit})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, catalogGlobalCandidate{chart: sqlc.HelmChart{
			ID:           row.ID,
			RepositoryID: row.RepositoryID,
			Name:         row.Name,
			DisplayName:  row.DisplayName,
			Description:  row.Description,
			IconUrl:      row.IconUrl,
			HomeUrl:      row.HomeUrl,
			Category:     row.Category,
			Keywords:     row.Keywords,
			Maintainers:  row.Maintainers,
			Deprecated:   row.Deprecated,
			CreatedAt:    row.CreatedAt,
			UpdatedAt:    row.UpdatedAt,
		}, matches: row.Matches})
	}
	return out, nil
}

func catalogGlobalWindowPage(rows []catalogGlobalCandidate, page cursorRequest) ([]sqlc.HelmChart, *paging.Cursor, error) {
	size := int(catalogGlobalWindowSize(page.limit))
	if !page.mode || size < 1 || len(rows) > size+1 {
		return nil, nil, errors.New("invalid catalog candidate window")
	}
	seen := map[uuid.UUID]bool{}
	for _, row := range rows {
		if row.chart.ID == uuid.Nil || row.chart.RepositoryID == uuid.Nil || seen[row.chart.ID] {
			return nil, nil, errors.New("invalid catalog candidate identity")
		}
		seen[row.chart.ID] = true
	}
	more := len(rows) > size
	if more {
		rows = rows[:size]
	}
	charts := []sqlc.HelmChart{}
	for _, row := range rows {
		if row.matches {
			charts = append(charts, row.chart)
		}
	}
	if !more {
		return charts, nil, nil
	}
	last := rows[len(rows)-1].chart
	if page.provided && page.cursor.ID == last.ID && page.cursor.Group != nil && *page.cursor.Group == last.RepositoryID && page.cursor.Text != nil && *page.cursor.Text == last.Name {
		return nil, nil, errors.New("catalog candidate did not advance")
	}
	next := paging.NewTextCursor(last.Name, last.ID, &last.RepositoryID, page.binding, page.seen+len(charts))
	return charts, &next, nil
}

func writeCatalogGlobalWindow(w http.ResponseWriter, r *http.Request, q catalogGlobalWindowQuerier, filter sqlc.CountFilteredHelmChartsParams, page cursorRequest) {
	rows, err := listCatalogGlobalWindow(r.Context(), q, filter, page)
	if err == nil {
		err = r.Context().Err()
	}
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.ListError, "Failed to inspect catalog chart window")
		return
	}
	charts, next, err := catalogGlobalWindowPage(rows, page)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.ListError, "Invalid catalog chart window")
		return
	}
	metadata, ok := page.metadata(w, r, len(charts), next, nil)
	if ok {
		paging.Write(w, charts, metadata)
	}
}
