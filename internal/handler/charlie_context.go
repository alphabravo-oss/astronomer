package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/charlie"
)

type CharlieContextSearcher interface {
	Search(context.Context, uuid.UUID, string, int32) ([]charlie.ContextSearchResult, error)
}

type CharlieContextHandler struct{ searcher CharlieContextSearcher }

func NewCharlieContextHandler(searcher CharlieContextSearcher) *CharlieContextHandler {
	if searcher == nil {
		return nil
	}
	return &CharlieContextHandler{searcher: searcher}
}

func (h *CharlieContextHandler) Search(w http.ResponseWriter, r *http.Request) {
	actor, ok := browserCharlieActor(w, r)
	if !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit, valid := boundedQueryInt(w, r, "limit", charlie.MaxContextSearchResults, 1, charlie.MaxContextSearchResults)
	if !valid {
		return
	}
	if len(query) == 1 || len(query) > 128 {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Charlie context query must be empty or between 2 and 128 characters")
		return
	}
	items, err := h.searcher.Search(r.Context(), mustUserID(actor), query, int32(limit))
	if err != nil {
		apiutil.WriteError(w, r, http.StatusForbidden, apiutil.Forbidden, "Charlie context search is denied")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"items": items})
}
