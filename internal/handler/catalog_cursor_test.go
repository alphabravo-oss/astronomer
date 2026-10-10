package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/google/uuid"
)

type recordingCatalogCursorQuerier struct {
	*minimalCatalogQuerier
	reads int
}

func (q *recordingCatalogCursorQuerier) ListFilteredHelmChartsAfter(context.Context, sqlc.ListFilteredHelmChartsAfterParams) ([]sqlc.HelmChart, error) {
	q.reads++
	return nil, nil
}

func TestCatalogCursorRejectsAuthenticatedWrongPositionBeforeSQL(t *testing.T) {
	q := &recordingCatalogCursorQuerier{minimalCatalogQuerier: &minimalCatalogQuerier{fakeProjectCatalogQuerier: newFakeProjectCatalogQuerier()}}
	h := NewCatalogHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: []rbac.RoleBinding{{RoleRules: []rbac.Rule{{Resource: "catalog", Verbs: []string{"read"}}}}}})
	codec, err := paging.NewCursorCodec("fixture-cursor-authority")
	if err != nil {
		t.Fatal(err)
	}
	h.SetCursorCodec(codec)
	actor := uuid.New()
	binding := paging.Binding("catalog-charts:v2", "name:asc,repository_id:asc,id:asc", actor.String(), "true", "", "", "", "", "")
	for _, position := range []paging.Cursor{paging.NewCursor(time.Now(), uuid.New(), binding, 20), paging.NewTextCursor("chart", uuid.New(), nil, binding, 20)} {
		token, err := codec.Encode(position)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/?cursor="+url.QueryEscape(token), nil)
		req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: actor.String()}))
		rec := httptest.NewRecorder()
		h.ListCharts(rec, req)
		if rec.Code != http.StatusBadRequest || q.reads != 0 {
			t.Fatalf("wrong authenticated shape status%d SQLreads%d", rec.Code, q.reads)
		}
	}
}

func TestCatalogLegacyRequestPreservesReleasedCursorBoundAndCount(t *testing.T) {
	fake := newFakeProjectCatalogQuerier()
	repo := uuid.New()
	fake.catalogs[repo] = sqlc.HelmRepositoryWithOwner{ID: repo}
	fake.chartsByRepo[repo] = []sqlc.HelmChart{
		{ID: uuid.New(), RepositoryID: repo, Name: strings.Repeat("界", 255)},
		{ID: uuid.New(), RepositoryID: repo, Name: strings.Repeat("界", 255) + "z"},
	}
	q := &recordingCatalogCursorQuerier{minimalCatalogQuerier: &minimalCatalogQuerier{fakeProjectCatalogQuerier: fake}}
	h := NewCatalogHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: []rbac.RoleBinding{{RoleRules: []rbac.Rule{{Resource: "catalog", Verbs: []string{"read"}}}}}})
	codec, err := paging.NewCursorCodec("fixture-cursor-authority")
	if err != nil {
		t.Fatal(err)
	}
	h.SetCursorCodec(codec)
	req := httptest.NewRequest(http.MethodGet, "/?limit=1", nil)
	req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: uuid.NewString()}))
	rec := httptest.NewRecorder()
	h.ListCharts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy status%d", rec.Code)
	}
	var response paging.Response[sqlc.HelmChart]
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Pagination.NextCursor != nil && len(*response.Pagination.NextCursor) > 1024 {
		t.Fatalf("released next_cursor maxLength1024 violated: length%d", len(*response.Pagination.NextCursor))
	}
	if response.Pagination.NextCursor != nil || response.Pagination.Total == nil || *response.Pagination.Total != 2 || !response.Pagination.HasMore {
		t.Fatal("absent cursor changed released counted offset metadata")
	}
}
