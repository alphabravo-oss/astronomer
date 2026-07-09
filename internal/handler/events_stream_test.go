package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/events"
)

// TEST-06: EventStreamHandler.Stream with no JWT wired accepts (dev mode)
// and writes SSE headers + connected comment.
func TestEventStream_UnauthedWhenNoJWT(t *testing.T) {
	bus := events.NewBus()
	h := NewEventStreamHandler(bus)
	// Cancel client quickly so Stream exits.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream/", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.Stream(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not exit")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type=%q", ct)
	}
	if !strings.Contains(rec.Body.String(), "connected") {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

// TEST-06: when JWT is wired without credentials, stream rejects.
func TestEventStream_RequiresAuthWhenJWTWired(t *testing.T) {
	bus := events.NewBus()
	h := NewEventStreamHandler(bus)
	jwt := auth.NewJWTManager("test-secret-key-for-stream", 15)
	h.SetAuth(jwt, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream/", nil)
	rec := httptest.NewRecorder()
	h.Stream(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
}

func TestEventStream_NilBusUnavailable(t *testing.T) {
	h := &EventStreamHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream/", nil)
	rec := httptest.NewRecorder()
	h.Stream(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rec.Code)
	}
}
