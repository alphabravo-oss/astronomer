package handler

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/google/uuid"
)

func TestCatalogPaginationCeilingMustProgressOrReject(t *testing.T) {
	fake := newFakeProjectCatalogQuerier()
	repository := seedGlobal(fake, "large")
	for i := 0; i < 10041; i++ {
		fake.chartsByRepo[repository] = append(fake.chartsByRepo[repository], sqlc.HelmChart{ID: uuid.New(), RepositoryID: repository, Name: fmt.Sprintf("chart-%05d", i)})
	}
	h := NewCatalogHandler(&minimalCatalogQuerier{fakeProjectCatalogQuerier: fake})
	first := httptest.NewRecorder()
	h.ListCharts(first, httptest.NewRequest("GET", "/api/v1/catalog/charts/?limit=20&offset=10000", nil))
	if first.Code != 200 {
		t.Fatalf("bounded legacy page: status%d", first.Code)
	}
	var page paging.Response[sqlc.HelmChart]
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Pagination.NextOffset == nil || *page.Pagination.NextOffset != 10020 || len(page.Data) != 20 {
		t.Fatalf("ceiling fixture metadata%+v count%d", page.Pagination, len(page.Data))
	}
	second := httptest.NewRecorder()
	h.ListCharts(second, httptest.NewRequest("GET", fmt.Sprintf("/api/v1/catalog/charts/?limit=20&offset=%d", *page.Pagination.NextOffset), nil))
	if second.Code == 400 {
		var rejection struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(second.Body.Bytes(), &rejection); err != nil {
			t.Fatal(err)
		}
		if rejection.Error.Code != "offset_not_supported" || rejection.Error.Message == "" {
			t.Fatalf("wrong rejection: %s", second.Body.String())
		}
		return
	}
	var next paging.Response[sqlc.HelmChart]
	if err := json.Unmarshal(second.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if second.Code != 200 || len(next.Data) == 0 || next.Data[0].ID == page.Data[0].ID {
		t.Fatalf("accepted continuation repeated ceiling page: status%d first=%s next=%s", second.Code, page.Data[0].Name, next.Data[0].Name)
	}
}

func TestCatalogPaginationRejectsUnsupportedOffsetBeforeQuery(t *testing.T) {
	q := &recordingChartSearchQuerier{clusterCatalogQuerier: &clusterCatalogQuerier{minimalCatalogQuerier: &minimalCatalogQuerier{fakeProjectCatalogQuerier: newFakeProjectCatalogQuerier()}}}
	h := NewCatalogHandler(q)
	for _, query := range []string{"offset=10020", "offset=18446744073709551615", "offset=1&offset=2", "cursor=foreign"} {
		response := httptest.NewRecorder()
		h.ListCharts(response, httptest.NewRequest("GET", "/api/v1/catalog/charts/?"+query, nil))
		if response.Code != 400 || q.calls != 0 {
			t.Fatalf("query%s status%d SQLcalls%d", query, response.Code, q.calls)
		}
	}
}
