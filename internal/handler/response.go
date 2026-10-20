package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// RespondJSON writes a JSON response wrapped in {"data": payload}.
func RespondJSON(w http.ResponseWriter, status int, payload any) {
	resp := map[string]any{"data": payload}
	writeJSON(w, status, resp)
}

// RespondJSONUnwrapped writes a JSON response without the {"data": ...} wrapper.
// Used for endpoints with a deliberately unwrapped contract, such as login
// token payloads and identity discovery.
func RespondJSONUnwrapped(w http.ResponseWriter, status int, payload any) {
	writeJSON(w, status, payload)
}

// RespondAcceptedOperation writes a durable-operation receipt and the standard
// polling headers clients need to recover progress after a disconnect.
func RespondAcceptedOperation(w http.ResponseWriter, location string, payload any) {
	w.Header().Set("Location", location)
	w.Header().Set("Retry-After", "2")
	RespondJSON(w, http.StatusAccepted, payload)
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

// maxPaginationOffset is the compatibility ceiling for legacy offset clients.
// Fleet endpoints use keyset cursors by default; no request may make
// PostgreSQL discard an unbounded number of rows.
const maxPaginationOffset = uint64(10_000)

// queryOffset parses the shared "offset" query parameter into the non-negative
// bounded range accepted by legacy sqlc pagination queries. Invalid and
// negative values start at the first page; oversized values clamp before SQL.
func queryOffset(r *http.Request) int {
	return int(queryOffset32(r))
}

// queryOffset32 keeps the SQL boundary in the same bounded parser as wire metadata.
func queryOffset32(r *http.Request) int32 {
	s := r.URL.Query().Get("offset")
	if s == "" {
		return 0
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		if numErr, ok := err.(*strconv.NumError); ok && numErr.Err == strconv.ErrRange {
			return int32(maxPaginationOffset)
		}
		return 0
	}
	if v > maxPaginationOffset {
		return int32(maxPaginationOffset)
	}
	return int32(v)
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

// queryLimit32 exposes the existing ordinary page limit as a checked SQL value.
func queryLimit32(r *http.Request, defaultLimit int32) int32 {
	return queryLimitMax32(r, defaultLimit, defaultLimitCap)
}

// queryLimitMax32 preserves legacy Atoi parsing and caller-selected limits while
// checking representability directly at the SQL boundary. The existing int
// helpers remain the source of pagination response metadata.
func queryLimitMax32(r *http.Request, defaultLimit, max int32) int32 {
	limit := queryLimitMax(r, int(defaultLimit), int(max))
	if limit < 1 {
		return 1
	}
	if limit > 1<<31-1 {
		return 1<<31 - 1
	}
	return int32(limit)
}

// queryLimitOffset parses the "limit"/"offset" pagination query params, clamping
// limit to [1, 200] (falling back to defaultLimit when missing, unparseable, or
// < 1) and offset to >= 0.
func queryLimitOffset(r *http.Request, defaultLimit int) (limit, offset int) {
	limit = queryLimit(r, defaultLimit)
	offset = queryOffset(r)
	return limit, offset
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
