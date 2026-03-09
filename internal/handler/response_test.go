package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestRespondPaginated(t *testing.T) {
	t.Run("middle page has next and previous", func(t *testing.T) {
		w := httptest.NewRecorder()

		items := []string{"a", "b", "c"}
		RespondPaginated(w, http.StatusOK, items, 100, 10, 20)

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
		if *body.Next != "?limit=10&offset=30" {
			t.Fatalf("expected next=?limit=10&offset=30, got %s", *body.Next)
		}
		if body.Previous == nil {
			t.Fatal("expected previous to be non-nil")
		}
		if *body.Previous != "?limit=10&offset=10" {
			t.Fatalf("expected previous=?limit=10&offset=10, got %s", *body.Previous)
		}
	})

	t.Run("first page has no previous", func(t *testing.T) {
		w := httptest.NewRecorder()

		RespondPaginated(w, http.StatusOK, []string{"a"}, 50, 10, 0)

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

		RespondPaginated(w, http.StatusOK, []string{"a"}, 25, 10, 20)

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
}
