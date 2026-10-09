package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
