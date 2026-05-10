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
		if correlationID := GetCorrelationID(r.Context()); correlationID != id {
			t.Fatalf("expected matching correlation ID, got %q and %q", correlationID, id)
		}
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, req)

	got := rr.Header().Get("X-Request-ID")
	if got == "" {
		t.Fatal("expected X-Request-ID response header, got empty string")
	}
	if correlation := rr.Header().Get("X-Correlation-Id"); correlation != got {
		t.Fatalf("expected matching X-Correlation-Id header, got %q and %q", correlation, got)
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
	if got := rr.Header().Get("X-Correlation-Id"); got != incoming {
		t.Fatalf("expected X-Correlation-Id header %q, got %q", incoming, got)
	}
	if ctxID != incoming {
		t.Fatalf("expected context request ID %q, got %q", incoming, ctxID)
	}
}

func TestRequestIDMiddleware_PrefersCorrelationID(t *testing.T) {
	const incoming = "corr-123"

	var ctxID string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID = GetCorrelationID(r.Context())
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Correlation-Id", incoming)
	req.Header.Set("X-Request-ID", "request-456")
	handler.ServeHTTP(rr, req)

	if ctxID != incoming {
		t.Fatalf("expected context correlation ID %q, got %q", incoming, ctxID)
	}
	if got := rr.Header().Get("X-Request-ID"); got != incoming {
		t.Fatalf("expected X-Request-ID header %q, got %q", incoming, got)
	}
	if got := rr.Header().Get("X-Correlation-Id"); got != incoming {
		t.Fatalf("expected X-Correlation-Id header %q, got %q", incoming, got)
	}
}

func TestRequestIDMiddleware_RejectsControlChars(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "bad\nvalue")
	handler.ServeHTTP(rr, req)

	got := rr.Header().Get("X-Request-ID")
	if got == "bad\nvalue" {
		t.Fatal("expected control-char request ID to be rejected")
	}
	if len(got) != 36 {
		t.Fatalf("expected regenerated UUID (len 36), got %q (len %d)", got, len(got))
	}
}

func TestRequestIDMiddleware_RejectsTooLong(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", string(long))
	handler.ServeHTTP(rr, req)

	got := rr.Header().Get("X-Request-ID")
	if len(got) == 300 {
		t.Fatal("expected overlong request ID to be rejected")
	}
	if len(got) != 36 {
		t.Fatalf("expected regenerated UUID (len 36), got %q (len %d)", got, len(got))
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
