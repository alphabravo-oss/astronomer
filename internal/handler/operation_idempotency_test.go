package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireOperationIdempotencyKey(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		want      bool
	}{
		{name: "missing"},
		{name: "control character", key: "bad\nkey"},
		{name: "valid", key: "operation-1", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/tools/install", nil)
			r.Header.Set("Idempotency-Key", tc.key)
			w := httptest.NewRecorder()
			if got := RequireOperationIdempotencyKey(w, r); got != tc.want {
				t.Fatalf("result = %v, want %v", got, tc.want)
			}
			if tc.want {
				return
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil || body.Error.Code != "invalid_request" {
				t.Fatalf("unexpected error envelope: code=%q err=%v", body.Error.Code, err)
			}
		})
	}
}

func TestRequireOperationIdempotencyKeyRejectsDuplicateHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/id/snapshots", nil)
	r.Header.Add("Idempotency-Key", "one")
	r.Header.Add("Idempotency-Key", "two")
	w := httptest.NewRecorder()
	if RequireOperationIdempotencyKey(w, r) {
		t.Fatal("duplicate Idempotency-Key headers were accepted")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want %d", w.Code, http.StatusBadRequest)
	}
}
