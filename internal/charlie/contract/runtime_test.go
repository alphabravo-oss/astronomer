package contract

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type runtimeAvailability bool

func (a runtimeAvailability) AllowsConfiguration() bool { return bool(a) }
func (a runtimeAvailability) AllowsRuntime() bool       { return bool(a) }

func testRuntime(server *httptest.Server, active *atomic.Bool) *Runtime {
	return &Runtime{
		client:      &Client{endpoint: server.URL, httpClient: server.Client()},
		active:      active.Load,
		now:         time.Now,
		turnTimeout: defaultTurnTimeout,
	}
}

func TestRuntimeRequiresActiveIntegration(t *testing.T) {
	if _, err := NewRuntimeClient(runtimeAvailability(false), "charlie-system", testBridgeServerIdentity, testTLSConfig()); err == nil {
		t.Fatal("runtime client constructed while inactive")
	}
	var active atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("inactive runtime made a network request")
	}))
	defer server.Close()
	runtime := testRuntime(server, &active)
	if err := runtime.DoJSON(context.Background(), http.MethodGet, "/health", "", nil, nil); err == nil {
		t.Fatal("inactive request succeeded")
	}
}

func TestRuntimeMutationsRequireIdempotencyAndRetrySafely(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Header.Get("Idempotency-Key") != "message-1" {
			t.Error("missing stable idempotency key")
		}
		if request.Header.Get("X-Charlie-Authorization-Ref") != "opaque-product-ref" {
			t.Error("missing opaque product authorization reference")
		}
		if calls.Load() < 3 {
			http.Error(w, "private upstream body", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"accepted":true}`)
	}))
	defer server.Close()
	var active atomic.Bool
	active.Store(true)
	runtime := testRuntime(server, &active)
	var result struct {
		Accepted bool `json:"accepted"`
	}
	if err := runtime.DoJSON(context.Background(), http.MethodPost, "/sessions/one/messages", "", map[string]string{"message": "secret"}, &result); err == nil {
		t.Fatal("mutation without idempotency key succeeded")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("unkeyed mutation reached bridge %d times", got)
	}
	if err := runtime.DoJSONAuthorized(context.Background(), http.MethodPost, "/sessions/one/messages", "message-1", "opaque-product-ref", map[string]string{"message": "secret"}, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Accepted || calls.Load() != 3 {
		t.Fatalf("result=%+v calls=%d", result, calls.Load())
	}
}

func TestRuntimeErrorsAreContentFreeAndCircuitBreak(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "certificate=/secret agent=https://central.invalid prompt=private", http.StatusBadRequest)
	}))
	defer server.Close()
	var active atomic.Bool
	active.Store(true)
	runtime := testRuntime(server, &active)
	for range 3 {
		err := runtime.DoJSON(context.Background(), http.MethodGet, "/status", "", nil, nil)
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "central") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
	before := calls.Load()
	err := runtime.DoJSON(context.Background(), http.MethodGet, "/status", "", nil, nil)
	var stable *StableError
	if !errors.As(err, &stable) || stable.Code != "bridge_circuit_open" {
		t.Fatalf("expected open circuit, got %v", err)
	}
	if calls.Load() != before {
		t.Fatal("open circuit made a network request")
	}
}

func TestRuntimeRejectsOversizedAndTrailingJSONResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/oversized":
			_, _ = io.WriteString(w, `{"value":"`+strings.Repeat("x", maxBridgeResponseBytes)+`"}`)
		default:
			_, _ = io.WriteString(w, `{"accepted":true} {"accepted":false}`)
		}
	}))
	defer server.Close()
	var active atomic.Bool
	active.Store(true)
	runtime := testRuntime(server, &active)
	var output map[string]any
	for _, path := range []string{"/oversized", "/trailing"} {
		if err := runtime.DoJSON(context.Background(), http.MethodGet, path, "", nil, &output); err == nil {
			t.Fatalf("invalid response from %s was accepted", path)
		}
	}
}

func TestStreamEnforcesTurnTimeoutAndCancelsBlockedResponse(t *testing.T) {
	cancelled := make(chan struct{}, 1)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Header.Get("X-Charlie-Authorization-Ref") != "opaque-product-ref" {
			t.Error("stream omitted product authorization")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
		select {
		case cancelled <- struct{}{}:
		default:
		}
	}))
	defer server.Close()
	var active atomic.Bool
	active.Store(true)
	runtime := testRuntime(server, &active)
	runtime.turnTimeout = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	if err := runtime.StreamEventsAuthorized(ctx, "session-one", "", "opaque-product-ref", func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if calls.Load() == 0 {
		t.Fatal("stream was never opened")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("turn timeout did not cancel the blocked stream")
	}
}

func TestConsumeSSEPreservesIDsAndBoundsFrames(t *testing.T) {
	input := ": heartbeat\nid: event-0001\nevent: turn.delta\ndata: {\"turn_id\":\"turn-7\"}\ndata: {\"delta\":\"ok\"}\n\n"
	var events []Event
	if err := consumeSSE(strings.NewReader(input), func(event Event) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != "event-0001" || events[0].Event != "turn.delta" || !strings.Contains(string(events[0].Data), "turn-7") {
		t.Fatalf("event not preserved: %+v", events)
	}
	oversized := "data: " + strings.Repeat("x", maxBridgeEventBytes+1) + "\n\n"
	if err := consumeSSE(strings.NewReader(oversized), func(Event) error { return nil }); err == nil {
		t.Fatal("oversized event accepted")
	}
}

func TestSafeBridgePathCannotSelectAlternateTransport(t *testing.T) {
	for _, path := range []string{"https://central.example/session", "//central.example/session", "/sessions?url=https://other", "/sessions?cursor=", "/sessions?limit=0", "/sessions?limit=101", "/sessions#fragment", "sessions"} {
		if safeBridgePath(path) {
			t.Errorf("unsafe path accepted: %q", path)
		}
	}
	if !safeBridgePath("/sessions/session-1/history") || !safeBridgePath("/sessions/session-1/history?cursor=history%3A7&limit=50") {
		t.Fatal("valid bridge path rejected")
	}
}
