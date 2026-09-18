package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type searchableUserStore struct {
	ResourceQuerier
	params sqlc.SearchUsersParams
	count  string
}

func (s *searchableUserStore) SearchUsers(_ context.Context, params sqlc.SearchUsersParams) ([]sqlc.User, error) {
	s.params = params
	return []sqlc.User{{ID: uuid.New(), Username: "sre.alice", Email: "alice@example.com", IsActive: true}}, nil
}

func (s *searchableUserStore) CountSearchUsers(_ context.Context, search string) (int64, error) {
	s.count = search
	return 1, nil
}

func TestListUsersServerSideSearchIsPaginated(t *testing.T) {
	store := &searchableUserStore{}
	h := NewResourceHandlerWithQueries(store, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users?search=%20Alice%20&limit=7&offset=14", nil)
	rec := httptest.NewRecorder()

	h.ListUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.params.Search != "Alice" || store.params.PageLimit != 7 || store.params.PageOffset != 14 {
		t.Fatalf("search params=%+v", store.params)
	}
	if store.count != "Alice" {
		t.Fatalf("count search=%q", store.count)
	}
}
