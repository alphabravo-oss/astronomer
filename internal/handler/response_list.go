package handler

import (
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/pagination"
)

// pageWindow applies the repository's single limit/offset policy to an
// in-memory result set. Callers that cannot push pagination into their
// upstream (for example Kubernetes list responses) use this instead of
// reimplementing bounds checks and pagination metadata.
func pageWindow[T any](r *http.Request, items []T) ([]T, pagination.Metadata) {
	limit, offset := queryLimitOffset(r, 20)
	total := len(items)
	start := min(offset, total)
	end := min(start+limit, total)
	return items[start:end], pagination.Exact(total, limit, offset, end-start)
}
