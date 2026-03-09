package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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
// It extracts limit and offset from the request's query parameters,
// defaulting to limit=20 and offset=0. Always responds with status 200.
func RespondPaginated(w http.ResponseWriter, r *http.Request, items any, total int64) {
	limit := queryInt(r, "limit", 20)
	offset := queryInt(r, "offset", 0)

	resp := paginatedResponse{
		Data:  items,
		Count: total,
	}

	if offset+limit < int(total) {
		next := fmt.Sprintf("%s?limit=%d&offset=%d", r.URL.Path, limit, offset+limit)
		resp.Next = &next
	}

	if offset > 0 {
		prevOffset := offset - limit
		if prevOffset < 0 {
			prevOffset = 0
		}
		prev := fmt.Sprintf("%s?limit=%d&offset=%d", r.URL.Path, limit, prevOffset)
		resp.Previous = &prev
	}

	writeJSON(w, http.StatusOK, resp)
}

// queryInt extracts an integer query parameter with a default fallback.
func queryInt(r *http.Request, key string, defaultVal int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
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
