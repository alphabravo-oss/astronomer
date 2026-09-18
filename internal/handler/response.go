package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/google/uuid"
)

// statusClientClosedRequest is nginx's conventional code for a request whose
// caller disconnected before a response could be produced. It is deliberately
// non-standard and never reaches a still-listening browser; recording it keeps
// canceled route-transition reads out of the platform's 5xx error signal.
const statusClientClosedRequest = 499

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

// RespondError writes the standard JSON error envelope.
func RespondError(w http.ResponseWriter, status int, code, message string) {
	requestID := ""
	if status >= http.StatusInternalServerError {
		requestID = uuid.NewString()
		slog.Error("request failed", "request_id", requestID, "status", status, "code", code, "internal_detail", message)
		message = publicServerErrorMessage(status)
	}
	errObj := map[string]string{"code": code, "message": message}
	if requestID != "" {
		errObj["request_id"] = requestID
	}
	resp := map[string]any{"error": errObj}
	writeJSON(w, status, resp)
}

// RespondRequestError writes a JSON error response that includes the request
// correlation identifier when RequestID middleware has populated one.
func RespondRequestError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if r != nil && errors.Is(r.Context().Err(), context.Canceled) {
		w.WriteHeader(statusClientClosedRequest)
		return
	}
	requestID := ""
	if r != nil {
		requestID = reqctx.RequestID(r.Context())
	}
	if status >= http.StatusInternalServerError {
		if requestID == "" {
			requestID = uuid.NewString()
		}
		slog.Error("request failed", "request_id", requestID, "status", status, "code", code, "internal_detail", message)
		message = publicServerErrorMessage(status)
	}
	errObj := map[string]string{
		"code":    code,
		"message": message,
	}
	if requestID != "" {
		errObj["request_id"] = requestID
	}
	writeJSON(w, status, map[string]any{"error": errObj})
}

func publicServerErrorMessage(status int) string {
	switch status {
	case http.StatusBadGateway:
		return "An upstream service could not complete the request"
	case http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "The service is temporarily unavailable"
	default:
		return "The request could not be completed"
	}
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
	s := r.URL.Query().Get("offset")
	if s == "" {
		return 0
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		if numErr, ok := err.(*strconv.NumError); ok && numErr.Err == strconv.ErrRange {
			return int(maxPaginationOffset)
		}
		return 0
	}
	if v > maxPaginationOffset {
		return int(maxPaginationOffset)
	}
	return int(v)
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
	limit = queryLimit(r, defaultLimit)
	offset = queryOffset(r)
	return limit, offset
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
