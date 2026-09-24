package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type agentSearchFake struct {
	*fakeClusterAgentQuerier
	search string
	offset int32
}

func (q *agentSearchFake) CountClustersFiltered(_ context.Context, arg sqlc.CountClustersFilteredParams) (int64, error) {
	q.search = arg.FilterSearch
	return 101, nil
}
func (q *agentSearchFake) ListClustersFiltered(_ context.Context, arg sqlc.ListClustersFilteredParams) ([]sqlc.Cluster, error) {
	q.search, q.offset = arg.FilterSearch, arg.QueryOffset
	return q.clusters, nil
}
func (q *agentSearchFake) ListClustersFilteredAfter(_ context.Context, arg sqlc.ListClustersFilteredAfterParams) ([]sqlc.Cluster, error) {
	q.search = arg.FilterSearch
	return q.clusters, nil
}

func TestClusterAgentSearchUsesCanonicalFilterBeforePaging(t *testing.T) {
	q := &agentSearchFake{fakeClusterAgentQuerier: &fakeClusterAgentQuerier{clusters: []sqlc.Cluster{{ID: uuid.New(), Name: "late-match"}}}}
	h := NewClusterAgentHandler(q)
	response := httptest.NewRecorder()
	h.List(response, httptest.NewRequest(http.MethodGet, "/api/v1/cluster-agents/?search=%20late%20&offset=100&limit=1", nil))
	var page clusterAgentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || q.search != "late" || q.offset != 100 || page.Summary.TotalClusters != 101 || len(page.Data) != 1 {
		t.Fatalf("filter/page mismatch: query=%+v page=%+v status=%d", q, page, response.Code)
	}
}

func TestClusterAgentSearchCursorCannotChangeFilter(t *testing.T) {
	now := time.Now()
	q := &agentSearchFake{fakeClusterAgentQuerier: &fakeClusterAgentQuerier{clusters: []sqlc.Cluster{{ID: uuid.New(), Name: "one", CreatedAt: now}, {ID: uuid.New(), Name: "two", CreatedAt: now.Add(-time.Second)}}}}
	h := NewClusterAgentHandler(q)
	response := httptest.NewRecorder()
	h.List(response, httptest.NewRequest(http.MethodGet, "/api/v1/cluster-agents/?search=one&limit=1", nil))
	var page clusterAgentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Pagination.NextCursor == nil {
		t.Fatal("continuation missing")
	}
	for _, search := range []string{"two", ""} {
		response = httptest.NewRecorder()
		h.List(response, httptest.NewRequest(http.MethodGet, "/api/v1/cluster-agents/?search="+search+"&cursor="+*page.Pagination.NextCursor, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("changed search returned %d", response.Code)
		}
	}
}

func TestClusterAgentSearchRejectsInvalidOrUnwiredQueries(t *testing.T) {
	h := NewClusterAgentHandler(&fakeClusterAgentQuerier{})
	for _, test := range []struct {
		query  string
		status int
	}{
		{"search=one", 503}, {"search=one&search=two", 400}, {"search=" + strings.Repeat("a", 201), 400},
	} {
		response := httptest.NewRecorder()
		h.List(response, httptest.NewRequest(http.MethodGet, "/api/v1/cluster-agents/?"+test.query, nil))
		if response.Code != test.status {
			t.Fatalf("status=%d want=%d", response.Code, test.status)
		}
	}
}
