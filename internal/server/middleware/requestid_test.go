package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDMiddleware_GeneratesID(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := GetRequestID(r.Context())
		if id == "" {
			t.Fatal("expected request ID in context, got empty string")
		}
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, req)

	got := rr.Header().Get("X-Request-ID")
	if got == "" {
		t.Fatal("expected X-Request-ID response header, got empty string")
	}
	// Basic UUID length check: 8-4-4-4-12 = 36 chars.
	if len(got) != 36 {
		t.Fatalf("expected UUID-length request ID, got %q (len %d)", got, len(got))
	}
}

func TestRequestIDMiddleware_ReusesID(t *testing.T) {
	const incoming = "test-123"

	var ctxID string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID = GetRequestID(r.Context())
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", incoming)
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Request-ID"); got != incoming {
		t.Fatalf("expected X-Request-ID header %q, got %q", incoming, got)
	}
	if ctxID != incoming {
		t.Fatalf("expected context request ID %q, got %q", incoming, ctxID)
	}
}

func TestGetRequestID(t *testing.T) {
	t.Run("with value", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), requestIDKey, "abc-456")
		if got := GetRequestID(ctx); got != "abc-456" {
			t.Fatalf("expected %q, got %q", "abc-456", got)
		}
	})

	t.Run("without value", func(t *testing.T) {
		if got := GetRequestID(context.Background()); got != "" {
			t.Fatalf("expected empty string, got %q", got)
		}
	})
}
