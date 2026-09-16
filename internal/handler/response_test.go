package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func TestRespondJSON(t *testing.T) {
	w := httptest.NewRecorder()

	payload := map[string]string{"name": "test"}
	RespondJSON(w, http.StatusOK, payload)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %s", ct)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'data' wrapper object, got %#v", body)
	}
	if data["name"] != "test" {
		t.Fatalf("expected name=test, got %v", data["name"])
	}
}

func TestRespondError(t *testing.T) {
	w := httptest.NewRecorder()

	RespondError(w, http.StatusBadRequest, "validation_error", "field is required")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %s", ct)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'error' wrapper object, got %#v", body)
	}
	if errObj["code"] != "validation_error" {
		t.Fatalf("expected code=validation_error, got %v", errObj["code"])
	}
	if errObj["message"] != "field is required" {
		t.Fatalf("expected message='field is required', got %v", errObj["message"])
	}
}

func TestRespondAcceptedOperation(t *testing.T) {
	w := httptest.NewRecorder()
	RespondAcceptedOperation(w, "/api/v1/tools/operations/op-1/", map[string]any{"id": "op-1"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
	}
	if got := w.Header().Get("Location"); got != "/api/v1/tools/operations/op-1/" {
		t.Fatalf("Location = %q", got)
	}
	if got := w.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q", got)
	}
	var body struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil || body.Data.ID != "op-1" {
		t.Fatalf("accepted operation envelope id=%q err=%v", body.Data.ID, err)
	}
}

func TestRespondRequestErrorIncludesRequestID(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "req-123")

	handler := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		RespondRequestError(w, r, http.StatusForbidden, "permission_denied", "not allowed")
	}))
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'error' wrapper object, got %#v", body)
	}
	if errObj["request_id"] != "req-123" {
		t.Fatalf("expected request_id=req-123, got %v", errObj["request_id"])
	}
}

func TestRespondRequestErrorRedactsInternalDetails(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	RespondRequestError(w, r, http.StatusInternalServerError, "db_error", "password=secret host=internal-db:5432")

	var body struct {
		Error map[string]string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error["message"] != "The request could not be completed" {
		t.Fatalf("public message = %q", body.Error["message"])
	}
	if body.Error["request_id"] == "" {
		t.Fatal("internal error did not include an opaque request_id")
	}
	if body.Error["message"] == "password=secret host=internal-db:5432" {
		t.Fatal("internal error detail leaked into the response")
	}
}

func TestPageResponseUsesAppliedWindow(t *testing.T) {
	for _, tc := range []struct {
		name, query   string
		total         int64
		items         []string
		limit, offset int
		next          *int
	}{
		{name: "middle", query: "?limit=10&offset=20", total: 100, items: []string{"a", "b", "c"}, limit: 10, offset: 20, next: intPointer(23)},
		{name: "last", query: "?limit=10&offset=20", total: 21, items: []string{"a"}, limit: 10, offset: 20},
		{name: "past end", query: "?limit=10&offset=100", total: 21, limit: 10, offset: 100},
		{name: "default", total: 50, items: []string{"a"}, limit: 20, next: intPointer(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/items"+tc.query, nil)
			limit, offset := queryLimitOffset(r, 20)
			w := httptest.NewRecorder()
			paging.Write(w, tc.items, paging.Exact(tc.total, limit, offset, len(tc.items)))
			var response paging.Response[string]
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			p := response.Pagination
			if w.Code != http.StatusOK || response.Data == nil || p.Total == nil || *p.Total != tc.total || p.Limit != tc.limit || p.Offset != tc.offset {
				t.Fatalf("response = %+v, metadata = %+v", response, p)
			}
			if (p.NextOffset == nil) != (tc.next == nil) || (tc.next != nil && *tc.next != *p.NextOffset) {
				t.Fatalf("next = %v, want %v", p.NextOffset, tc.next)
			}
		})
	}
}

func intPointer(value int) *int { return &value }

func TestQueryOffset(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  int
	}{
		{name: "missing", want: 0},
		{name: "valid", query: "?offset=42", want: 42},
		{name: "negative", query: "?offset=-1", want: 0},
		{name: "invalid", query: "?offset=nope", want: 0},
		{name: "int32 max", query: "?offset=2147483647", want: 2147483647},
		{name: "oversized", query: "?offset=9223372036854775807", want: 2147483647},
		{name: "beyond uint64", query: "?offset=999999999999999999999999999", want: 2147483647},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/items"+tt.query, nil)
			if got := queryOffset(r); got != tt.want {
				t.Fatalf("queryOffset() = %d, want %d", got, tt.want)
			}
		})
	}
}
