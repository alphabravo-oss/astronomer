package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// RespondJSON writes a JSON response wrapped in {"data": payload}.
func RespondJSON(w http.ResponseWriter, status int, payload any) {
	resp := map[string]any{"data": payload}
	writeJSON(w, status, resp)
}

// RespondError writes a JSON error response matching DRF format.
func RespondError(w http.ResponseWriter, status int, code, message string) {
	resp := map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	}
	writeJSON(w, status, resp)
}

// RespondPaginated writes a paginated JSON response matching DRF list format.
func RespondPaginated(w http.ResponseWriter, status int, items any, total int64, limit, offset int) {
	resp := paginatedResponse{
		Data:  items,
		Count: total,
	}

	if offset+limit < int(total) {
		next := fmt.Sprintf("?limit=%d&offset=%d", limit, offset+limit)
		resp.Next = &next
	}

	if offset > 0 {
		prevOffset := offset - limit
		if prevOffset < 0 {
			prevOffset = 0
		}
		prev := fmt.Sprintf("?limit=%d&offset=%d", limit, prevOffset)
		resp.Previous = &prev
	}

	writeJSON(w, status, resp)
}

type paginatedResponse struct {
	Data     any    `json:"data"`
	Count    int64  `json:"count"`
	Next     *string `json:"next"`
	Previous *string `json:"previous"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
