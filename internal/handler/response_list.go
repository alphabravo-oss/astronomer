package handler

import "net/http"

// Pagination describes the page of a list response emitted by RespondList.
//
// It is a forward-looking, self-describing alternative to the DRF-style
// {"data","count","next","previous"} envelope produced by RespondPaginated.
// The "data" key still carries the array verbatim, so consumers that only read
// data[] remain backward compatible; "pagination" is purely additive metadata.
type Pagination struct {
	// Total is the total number of items across all pages. When a COUNT query
	// is unavailable, callers set this to the length of the current page.
	Total int `json:"total"`
	// Limit is the page size that was applied.
	Limit int `json:"limit"`
	// Offset is the zero-based index of the first item on this page.
	Offset int `json:"offset"`
	// HasMore is true when more items exist beyond this page.
	HasMore bool `json:"has_more"`
	// NextOffset is the offset to request for the next page, or nil when this
	// is the last page.
	NextOffset *int `json:"next_offset"`
}

// NewPagination builds a Pagination from the page parameters and total count,
// computing HasMore and NextOffset. pageLen is the number of items actually
// returned on this page.
func NewPagination(total, limit, offset, pageLen int) Pagination {
	p := Pagination{
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}
	if offset+pageLen < total {
		p.HasMore = true
		next := offset + pageLen
		p.NextOffset = &next
	}
	return p
}

// RespondList writes a list response of the shape:
//
//	{"data": [...], "pagination": {...}}
//
// The "data" key holds the items array unchanged, preserving backward
// compatibility with the bare {"data": [...]} shape. Always responds 200.
func RespondList(w http.ResponseWriter, items any, pagination Pagination) {
	writeJSON(w, http.StatusOK, map[string]any{
		"data":       items,
		"pagination": pagination,
	})
}
