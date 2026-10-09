package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type inhibitionIntegrityQueries struct {
	AlertingQuerier
	rows []sqlc.AlertInhibition
}

func (q *inhibitionIntegrityQueries) ListAlertInhibitions(context.Context, sqlc.ListAlertInhibitionsParams) ([]sqlc.AlertInhibition, error) {
	return q.rows, nil
}

func (q *inhibitionIntegrityQueries) GetAlertInhibitionByID(context.Context, uuid.UUID) (sqlc.AlertInhibition, error) {
	return q.rows[0], nil
}

func (q *inhibitionIntegrityQueries) CountAlertInhibitions(context.Context) (int64, error) {
	return int64(len(q.rows)), nil
}

func TestInhibitionReadersRejectCorruptStoredJSON(t *testing.T) {
	valid := sqlc.AlertInhibition{ID: uuid.New(), SourceMatchers: []byte(`[]`), TargetMatchers: []byte(`[]`), EqualLabels: []byte(`[]`)}
	corrupt := valid
	corrupt.ID = uuid.New()
	corrupt.EqualLabels = []byte(`{"credential":"stored-sensitive-fixture"}`)
	for _, endpoint := range []string{"list", "get"} {
		t.Run(endpoint, func(t *testing.T) {
			q := &inhibitionIntegrityQueries{rows: []sqlc.AlertInhibition{corrupt, valid}}
			h := NewAlertingHandler(q)
			router := chi.NewRouter()
			router.Get("/", h.ListInhibitions)
			router.Get("/{id}", h.GetInhibition)
			path := "/"
			if endpoint == "get" {
				path += corrupt.ID.String()
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), `"code":"internal_error"`) {
				t.Fatalf("corrupt row fabricated a valid response: status=%d body=%s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "stored-sensitive-fixture") {
				t.Fatal("stored payload leaked in error response")
			}
		})
	}
}
