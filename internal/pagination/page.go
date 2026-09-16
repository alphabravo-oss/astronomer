// Package pagination defines the canonical collection response contract.
package pagination

import (
	"encoding/json"
	"net/http"
)

// Metadata describes the actual authorized page. Total is omitted unless an
// exact filtered count is known; a page length is never a substitute for it.
type Metadata struct {
	Total      *int64 `json:"total,omitempty"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
	HasMore    bool   `json:"has_more"`
	NextOffset *int   `json:"next_offset"`
}

type Response[T any] struct {
	Data       []T      `json:"data"`
	Pagination Metadata `json:"pagination"`
}

// Exact describes a page from a counted query or an authorized in-memory set.
func Exact[N ~int | ~int32 | ~int64](total N, limit, offset, pageLen int) Metadata {
	count := max(int64(total), 0)
	metadata := Uncounted(limit, offset, pageLen, int64(offset)+int64(pageLen) < count)
	metadata.Total = &count
	return metadata
}

// Uncounted describes a page whose continuation was determined by its producer
// (for example by fetching one extra row), without fabricating a total.
func Uncounted(limit, offset, pageLen int, hasMore bool) Metadata {
	metadata := Metadata{Limit: max(limit, 1), Offset: max(offset, 0)}
	if hasMore && pageLen > 0 {
		next := metadata.Offset + pageLen
		metadata.HasMore = true
		metadata.NextOffset = &next
	}
	return metadata
}

// FromPage is for bounded queries without COUNT or a sentinel row. A full page
// offers continuation; a short or empty page proves exhaustion.
func FromPage(limit, offset, pageLen int) Metadata {
	return Uncounted(limit, offset, pageLen, limit > 0 && pageLen >= limit)
}

// Write emits the only list envelope. Empty pages are arrays, never JSON null.
func Write[T any](w http.ResponseWriter, items []T, metadata Metadata) {
	if items == nil {
		items = []T{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(Response[T]{Data: items, Pagination: metadata})
}
