package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/google/uuid"
)

type contextSearcherFake struct {
	actor uuid.UUID
	query string
	limit int32
}

func (f *contextSearcherFake) Search(_ context.Context, actor uuid.UUID, query string, limit int32) ([]charlie.ContextSearchResult, error) {
	f.actor, f.query, f.limit = actor, query, limit
	return []charlie.ContextSearchResult{{Type: "agent_connection_record", ID: uuid.NewString(), RequiredVerb: "read", Label: "Agent", Summary: "Astronomer-owned connection metadata"}}, nil
}

func TestCharlieContextSearchRequiresBrowserAndReturnsBoundedItems(t *testing.T) {
	actor := uuid.New()
	searcher := &contextSearcherFake{}
	request := authenticatedCharlieRequest(http.MethodGet, "/?q=agent&limit=10", "", actor, "jwt")
	request.Header.Del("Content-Type")
	recorder := httptest.NewRecorder()
	NewCharlieContextHandler(searcher).Search(recorder, request)
	if recorder.Code != http.StatusOK || searcher.actor != actor || searcher.query != "agent" || searcher.limit != 10 {
		t.Fatalf("status=%d searcher=%#v body=%s", recorder.Code, searcher, recorder.Body.String())
	}

	apiToken := authenticatedCharlieRequest(http.MethodGet, "/?q=agent", "", actor, "api_token")
	apiToken.Header.Del("Content-Type")
	denied := httptest.NewRecorder()
	NewCharlieContextHandler(searcher).Search(denied, apiToken)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("API token context search status=%d", denied.Code)
	}
}
