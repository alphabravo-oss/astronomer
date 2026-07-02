package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// TestHandleStreamStopCancelsWatch verifies that a MsgK8sStreamStop-driven
// HandleStreamStop cancels the in-flight kube-apiserver watch so
// HandleStreamRequest drains and returns instead of leaking a goroutine +
// apiserver watch forever. Before the per-stream cancel wiring, the upstream
// (which holds the connection open) would keep HandleStreamRequest blocked on
// resp.Body.Read and this test would time out.
func TestHandleStreamStopCancelsWatch(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("test server ResponseWriter is not a Flusher")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"type":"ADDED","object":{}}` + "\n"))
		fl.Flush()
		close(started)
		// Hold the watch open like a real long-poll until the agent cancels
		// the request context (via HandleStreamStop).
		<-r.Context().Done()
	}))
	defer server.Close()

	proxy := &K8sProxy{
		restConfig: &rest.Config{Host: server.URL},
		httpClient: server.Client(),
		log:        slog.Default(),
		streams:    make(map[string]context.CancelFunc),
	}

	var mu sync.Mutex
	gotEnd := false
	sendFn := func(m *protocol.Message) error {
		if m.Type != protocol.MsgK8sStreamFrame {
			return nil
		}
		var f protocol.K8sStreamFrame
		if err := json.Unmarshal(m.Payload, &f); err != nil {
			return nil
		}
		if f.Kind == protocol.K8sStreamFrameEnd {
			mu.Lock()
			gotEnd = true
			mu.Unlock()
		}
		return nil
	}

	payload, err := json.Marshal(protocol.K8sRequestPayload{Method: http.MethodGet, Path: "/api/v1/pods?watch=true"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	msg := &protocol.Message{StreamID: "stream-1", Payload: payload}

	done := make(chan error, 1)
	go func() {
		done <- proxy.HandleStreamRequest(context.Background(), msg, sendFn)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream watch never started")
	}

	// The server-side consumer would send this when its client disconnects.
	if err := proxy.HandleStreamStop(&protocol.Message{StreamID: "stream-1"}); err != nil {
		t.Fatalf("HandleStreamStop: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("HandleStreamRequest returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HandleStreamRequest did not return after stop — watch/goroutine leaked")
	}

	mu.Lock()
	defer mu.Unlock()
	if !gotEnd {
		t.Fatal("expected an end frame after stop-driven cancellation")
	}
}

// TestHandleStreamStopUnknownStreamIsNoop ensures a stop for a stream the agent
// does not know about is a harmless no-op (matches LogHandler.HandleLogStop).
func TestHandleStreamStopUnknownStreamIsNoop(t *testing.T) {
	proxy := &K8sProxy{log: slog.Default(), streams: make(map[string]context.CancelFunc)}
	if err := proxy.HandleStreamStop(&protocol.Message{StreamID: "does-not-exist"}); err != nil {
		t.Fatalf("stop for unknown stream should be a no-op, got: %v", err)
	}
}

// TestExecuteUpstreamCapsBodySize verifies the agent fails closed with a 413
// Status body instead of buffering an unbounded proxied response (which would
// OOM the agent on a huge non-paginated LIST), while a body under the cap still
// passes through unchanged.
func TestExecuteUpstreamCapsBodySize(t *testing.T) {
	var respBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBody)
	}))
	defer server.Close()

	proxy := &K8sProxy{
		restConfig: &rest.Config{Host: server.URL},
		httpClient: server.Client(),
		log:        slog.Default(),
	}

	old := maxK8sResponseBodyBytes
	maxK8sResponseBodyBytes = 1024
	defer func() { maxK8sResponseBodyBytes = old }()

	payload, err := json.Marshal(protocol.K8sRequestPayload{Method: http.MethodGet, Path: "/api/v1/pods"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	// Over the cap → fail closed with 413 and a small body (NOT the giant body).
	respBody = bytes.Repeat([]byte("x"), 8192)
	body, status, _, err := proxy.executeUpstream(context.Background(), &protocol.Message{Payload: payload})
	if err != nil {
		t.Fatalf("executeUpstream (over cap): %v", err)
	}
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-cap status = %d, want 413", status)
	}
	if int64(len(body)) > maxK8sResponseBodyBytes {
		t.Fatalf("over-cap returned %d body bytes, want a small 413 status body", len(body))
	}

	// Under the cap → normal 200 with the full body preserved.
	respBody = bytes.Repeat([]byte("y"), 512)
	body, status, _, err = proxy.executeUpstream(context.Background(), &protocol.Message{Payload: payload})
	if err != nil {
		t.Fatalf("executeUpstream (under cap): %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("under-cap status = %d, want 200", status)
	}
	if !bytes.Equal(body, respBody) {
		t.Fatalf("under-cap body was altered; got %d bytes want %d", len(body), len(respBody))
	}
}
