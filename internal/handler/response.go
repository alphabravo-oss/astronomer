package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// RespondJSON writes a JSON response wrapped in {"data": payload}.
func RespondJSON(w http.ResponseWriter, status int, payload any) {
	resp := map[string]any{"data": payload}
	writeJSON(w, status, resp)
}

// RespondJSONUnwrapped writes a JSON response without the {"data": ...} wrapper.
// Used for endpoints that must match the Python/DRF response contract directly
// (bootstrap status, login token payload, auth/me, etc.) — the Next.js frontend
// reads these top-level keys without an unwrap layer.
func RespondJSONUnwrapped(w http.ResponseWriter, status int, payload any) {
	writeJSON(w, status, payload)
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

// RespondRequestError writes a JSON error response that includes the request
// correlation identifier when RequestID middleware has populated one.
func RespondRequestError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	errObj := map[string]string{
		"code":    code,
		"message": message,
	}
	if r != nil {
		if requestID := middleware.GetRequestID(r.Context()); requestID != "" {
			errObj["request_id"] = requestID
		}
	}
	writeJSON(w, status, map[string]any{"error": errObj})
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

// queryBool parses a boolean query param; accepts true/1/yes (case-insensitive).
// Anything else (including absent) is false.
func queryBool(r *http.Request, key string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key))) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

// defaultLimitCap is the hard upper bound applied to a client-supplied ?limit
// on ordinary list endpoints. It prevents ?limit=10000000 from materializing a
// whole table into memory (a cheap DoS / memory-amplification lever). Endpoints
// over the largest tables that legitimately page wider use queryLimitMax.
const defaultLimitCap = 200

// queryLimit parses the "limit" query param and clamps it to [1, defaultLimitCap],
// falling back to defaultLimit when missing/unparseable/<1. Use it instead of a
// raw queryInt(r, "limit", …) at any endpoint whose limit reaches SQL, so a
// hostile ?limit cannot amplify memory/DB load. A contract test enforces this.
func queryLimit(r *http.Request, defaultLimit int) int {
	return queryLimitMax(r, defaultLimit, defaultLimitCap)
}

// queryLimitMax is queryLimit with a caller-chosen ceiling, for endpoints over
// large tables (e.g. audit) that page wider than the default cap by design.
func queryLimitMax(r *http.Request, defaultLimit, max int) int {
	limit := queryInt(r, "limit", defaultLimit)
	if limit < 1 {
		limit = defaultLimit
	}
	if limit > max {
		limit = max
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

// queryLimitOffset parses the "limit"/"offset" pagination query params, clamping
// limit to [1, 200] (falling back to defaultLimit when missing, unparseable, or
// < 1) and offset to >= 0.
func queryLimitOffset(r *http.Request, defaultLimit int) (limit, offset int) {
	limit = queryInt(r, "limit", defaultLimit)
	if limit < 1 {
		limit = defaultLimit
	}
	if limit > 200 {
		limit = 200
	}
	offset = queryInt(r, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

type paginatedResponse struct {
	Data     any     `json:"data"`
	Count    int64   `json:"count"`
	Next     *string `json:"next"`
	Previous *string `json:"previous"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
