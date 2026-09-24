package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
)

// Keep the existing scoped catalog fixtures on the canonical filtered queries.
// SQL integration tests exercise the actual predicate and wildcard semantics.
func (q *fakeProjectCatalogQuerier) ListFilteredHelmCharts(_ context.Context, arg sqlc.ListFilteredHelmChartsParams) ([]sqlc.HelmChart, error) {
	rows := q.filteredCharts(arg.GlobalScope, arg.RepositoryIds, arg.SearchPattern)
	start := int(arg.QueryOffset)
	if start > len(rows) {
		start = len(rows)
	}
	end := start + int(arg.QueryLimit)
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], nil
}
func (q *fakeProjectCatalogQuerier) CountFilteredHelmCharts(_ context.Context, arg sqlc.CountFilteredHelmChartsParams) (int64, error) {
	return int64(len(q.filteredCharts(arg.GlobalScope, arg.RepositoryIds, arg.SearchPattern))), nil
}
func (q *fakeProjectCatalogQuerier) filteredCharts(global bool, ids []uuid.UUID, pattern string) []sqlc.HelmChart {
	selected := map[uuid.UUID]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	rows := []sqlc.HelmChart{}
	needle := strings.ToLower(strings.Trim(pattern, "%"))
	for repo, charts := range q.chartsByRepo {
		catalog := q.catalogs[repo]
		allowed := selected[repo]
		if global {
			allowed = !catalog.OwnerProjectID.Valid
		}
		if !allowed {
			continue
		}
		for _, chart := range charts {
			if needle == "" || strings.Contains(strings.ToLower(chart.Name+chart.DisplayName+chart.Description), needle) {
				rows = append(rows, chart)
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

type recordingChartSearchQuerier struct {
	*clusterCatalogQuerier
	list  sqlc.ListFilteredHelmChartsParams
	count sqlc.CountFilteredHelmChartsParams
	calls int
}

func (q *recordingChartSearchQuerier) ListFilteredHelmCharts(ctx context.Context, arg sqlc.ListFilteredHelmChartsParams) ([]sqlc.HelmChart, error) {
	q.list = arg
	q.calls++
	return q.fakeProjectCatalogQuerier.ListFilteredHelmCharts(ctx, arg)
}
func (q *recordingChartSearchQuerier) CountFilteredHelmCharts(ctx context.Context, arg sqlc.CountFilteredHelmChartsParams) (int64, error) {
	q.count = arg
	return q.fakeProjectCatalogQuerier.CountFilteredHelmCharts(ctx, arg)
}

func TestCatalogBrowseSearchReachesSQLBeforePagination(t *testing.T) {
	fake := newFakeProjectCatalogQuerier()
	project, cluster, foreignProject := uuid.New(), uuid.New(), uuid.New()
	fake.projects[project] = sqlc.Project{ID: project, ClusterID: cluster}
	visible := seedOwned(fake, "own", project)
	foreign := seedOwned(fake, "foreign", foreignProject)
	for i := 0; i < 60; i++ {
		fake.chartsByRepo[visible] = append(fake.chartsByRepo[visible], sqlc.HelmChart{ID: uuid.New(), RepositoryID: visible, Name: "unrelated"})
	}
	fake.chartsByRepo[visible] = append(fake.chartsByRepo[visible], sqlc.HelmChart{ID: uuid.New(), RepositoryID: visible, Name: "zz-wanted-chart"})
	fake.chartsByRepo[foreign] = []sqlc.HelmChart{{ID: uuid.New(), RepositoryID: foreign, Name: "foreign-wanted-chart"}}
	q := &recordingChartSearchQuerier{clusterCatalogQuerier: &clusterCatalogQuerier{minimalCatalogQuerier: &minimalCatalogQuerier{fakeProjectCatalogQuerier: fake}}}
	h := NewCatalogHandler(q)
	for _, scope := range []string{"project_id=" + project.String(), "cluster_id=" + cluster.String()} {
		req := httptest.NewRequest("GET", "/catalog/charts/?"+scope+"&search=WANTED&limit=1", nil)
		w := httptest.NewRecorder()
		h.ListCharts(w, req)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "zz-wanted-chart") || strings.Contains(w.Body.String(), "foreign-wanted-chart") {
			t.Fatalf("scope%s status%d: %s", scope, w.Code, w.Body.String())
		}
		if q.list.SearchPattern != "%WANTED%" || q.count.SearchPattern != q.list.SearchPattern || q.list.GlobalScope || len(q.list.RepositoryIds) != 1 || q.list.RepositoryIds[0] != visible || q.list.QueryLimit != 1 {
			t.Fatalf("incorrect SQL predicates: list%+v count%+v", q.list, q.count)
		}
	}
}

func TestCatalogBrowseSearchRejectsScopedTagBeforeAnyChartQuery(t *testing.T) {
	q := &recordingChartSearchQuerier{clusterCatalogQuerier: &clusterCatalogQuerier{minimalCatalogQuerier: &minimalCatalogQuerier{fakeProjectCatalogQuerier: newFakeProjectCatalogQuerier()}}}
	h := NewCatalogHandler(q)
	for _, scope := range []string{"project_id=", "cluster_id="} {
		for _, search := range []string{"", "&search=private"} {
			req := httptest.NewRequest("GET", "/catalog/charts/?"+scope+uuid.NewString()+"&tag=mesh"+search, nil)
			w := httptest.NewRecorder()
			h.ListCharts(w, req)
			if w.Code != 400 || q.calls != 0 {
				t.Fatalf("scopedtag should reject withoutquery, status%d calls%d", w.Code, q.calls)
			}
		}
	}
	for _, query := range []string{"search=" + strings.Repeat("x", 257), "search=a&search=b"} {
		req := httptest.NewRequest(http.MethodGet, "/catalog/charts/?"+query, nil)
		w := httptest.NewRecorder()
		h.ListCharts(w, req)
		if w.Code != 400 || q.calls != 0 {
			t.Fatalf("invalid search should reject, status%d", w.Code)
		}
	}
	req := httptest.NewRequest("GET", "/catalog/charts/?search="+url.QueryEscape(`50%_\value`), nil)
	w := httptest.NewRecorder()
	h.ListCharts(w, req)
	if w.Code != 200 || q.list.SearchPattern != `%50\%\_\\value%` || q.count.SearchPattern != q.list.SearchPattern || !q.list.GlobalScope {
		t.Fatalf("literal pattern missing: status%d list%+v count%+v", w.Code, q.list, q.count)
	}
}

func TestCatalogBrowseSearchDeniesInaccessibleScopesBeforeChartQuery(t *testing.T) {
	q := &recordingChartSearchQuerier{clusterCatalogQuerier: &clusterCatalogQuerier{minimalCatalogQuerier: &minimalCatalogQuerier{fakeProjectCatalogQuerier: newFakeProjectCatalogQuerier()}}}
	h := NewCatalogHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: catalogReadBindings(uuid.New())})
	for _, scope := range []string{"cluster_id=", "project_id="} {
		request := authedCatalogReq("GET", "/catalog/charts/?"+scope+uuid.NewString()+"&search=private", nil)
		recorder := httptest.NewRecorder()
		h.ListCharts(recorder, request)
		if recorder.Code != 403 || q.calls != 0 {
			t.Fatalf("scope%s status%d querycalls%d", scope, recorder.Code, q.calls)
		}
	}
}
