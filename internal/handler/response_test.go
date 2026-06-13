package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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

func TestRespondPaginated(t *testing.T) {
	t.Run("middle page has next and previous", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/items?limit=10&offset=20", nil)

		items := []string{"a", "b", "c"}
		RespondPaginated(w, r, items, 100)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}

		var body struct {
			Data     []string `json:"data"`
			Count    int64    `json:"count"`
			Next     *string  `json:"next"`
			Previous *string  `json:"previous"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}

		if len(body.Data) != 3 {
			t.Fatalf("expected 3 items, got %d", len(body.Data))
		}
		if body.Count != 100 {
			t.Fatalf("expected count=100, got %d", body.Count)
		}
		if body.Next == nil {
			t.Fatal("expected next to be non-nil")
		}
		if *body.Next != "/api/items?limit=10&offset=30" {
			t.Fatalf("expected next=/api/items?limit=10&offset=30, got %s", *body.Next)
		}
		if body.Previous == nil {
			t.Fatal("expected previous to be non-nil")
		}
		if *body.Previous != "/api/items?limit=10&offset=10" {
			t.Fatalf("expected previous=/api/items?limit=10&offset=10, got %s", *body.Previous)
		}
	})

	t.Run("first page has no previous", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/items?limit=10&offset=0", nil)

		RespondPaginated(w, r, []string{"a"}, 50)

		var body struct {
			Next     *string `json:"next"`
			Previous *string `json:"previous"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}
		if body.Next == nil {
			t.Fatal("expected next to be non-nil")
		}
		if body.Previous != nil {
			t.Fatalf("expected previous to be nil, got %v", *body.Previous)
		}
	})

	t.Run("last page has no next", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/items?limit=10&offset=20", nil)

		RespondPaginated(w, r, []string{"a"}, 25)

		var body struct {
			Next     *string `json:"next"`
			Previous *string `json:"previous"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}
		if body.Next != nil {
			t.Fatalf("expected next to be nil, got %v", *body.Next)
		}
		if body.Previous == nil {
			t.Fatal("expected previous to be non-nil")
		}
	})

	t.Run("defaults to limit=20 offset=0 when no query params", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/items", nil)

		RespondPaginated(w, r, []string{"a"}, 50)

		var body struct {
			Next     *string `json:"next"`
			Previous *string `json:"previous"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}
		if body.Next == nil {
			t.Fatal("expected next to be non-nil")
		}
		if *body.Next != "/api/items?limit=20&offset=20" {
			t.Fatalf("expected next=/api/items?limit=20&offset=20, got %s", *body.Next)
		}
		if body.Previous != nil {
			t.Fatalf("expected previous to be nil, got %v", *body.Previous)
		}
	})
}
