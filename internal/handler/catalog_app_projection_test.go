package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type unavailableAppsProjection struct {
	*minimalCatalogQuerier
	failList bool
}

func (q unavailableAppsProjection) ListProjectedClusterApps(context.Context, sqlc.ListProjectedClusterAppsParams) ([]sqlc.ListProjectedClusterAppsRow, error) {
	if q.failList {
		return nil, errors.New("private backend diagnostic")
	}
	return []sqlc.ListProjectedClusterAppsRow{{ProjectedStatus: "installed"}}, nil
}
func (unavailableAppsProjection) CountProjectedClusterApps(context.Context, uuid.UUID) (int64, error) {
	return 0, errors.New("private backend diagnostic")
}
func TestClusterAppsFailsClosedOnProjectionReadError(t *testing.T) {
	t.Parallel()
	for _, failList := range []bool{true, false} {
		t.Run(map[bool]string{true: "list", false: "count"}[failList], func(t *testing.T) {
			t.Parallel()
			h := NewCatalogHandler(unavailableAppsProjection{failList: failList})
			r := chi.NewRouter()
			r.Get("/clusters/{cluster_id}/apps/", h.ListClusterApps)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/clusters/"+uuid.NewString()+"/apps/", nil))
			if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "private backend") || strings.Contains(w.Body.String(), "installed") {
				t.Fatalf("unexpected error response: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
