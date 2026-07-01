package tunnel

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// flushSignalWriter is an http.ResponseWriter that reports each Flush that
// carries buffered bytes, so a test can observe per-chunk delivery on a
// streaming response.
type flushSignalWriter struct {
	header  http.Header
	mu      sync.Mutex
	buf     bytes.Buffer
	status  int
	flushed chan struct{}
}

func (f *flushSignalWriter) Header() http.Header { return f.header }
func (f *flushSignalWriter) WriteHeader(s int)   { f.status = s }
func (f *flushSignalWriter) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.Write(b)
}
func (f *flushSignalWriter) Flush() {
	select {
	case f.flushed <- struct{}{}:
	default:
	}
}
func (f *flushSignalWriter) body() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.String()
}

// TestForwardToOwnerPodFlushesWatchChunks is the regression for the cross-pod
// watch stall. When nginx pins a ?watch=true request to the non-owner pod, that
// pod forwards to the owner and must flush each event to the client as it
// arrives. Before the fix it piped the body with io.Copy and a single trailing
// Flush, so small events were held in the front pod's bufio buffer until ~2KB
// accumulated or the watch ended — freezing the browser/ArgoCD watch.
//
// The upstream owner sends event 1, flushes, then BLOCKS until the test observes
// that event 1 reached the client. With the fix the first chunk is flushed
// immediately, the test unblocks the owner, and the forward completes. Under the
// old io.Copy behaviour the first chunk is never flushed until the whole body is
// copied — but the owner is blocked waiting for that flush, so the forward hangs
// and the test fails via timeout.
func TestForwardToOwnerPodFlushesWatchChunks(t *testing.T) {
	proceed := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream ResponseWriter is not a Flusher")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"type":"ADDED","object":{"metadata":{"name":"a"}}}` + "\n"))
		fl.Flush()
		<-proceed // wait until the client actually received the first event
		_, _ = w.Write([]byte(`{"type":"MODIFIED","object":{"metadata":{"name":"a"}}}` + "\n"))
		fl.Flush()
	}))
	defer upstream.Close()

	oldClient := proxyHTTPClient
	proxyHTTPClient = upstream.Client()
	t.Cleanup(func() { proxyHTTPClient = oldClient })

	clusterID := uuid.New().String()
	hub := NewHub(slog.Default())
	hub.SetLocator(NewFakeLocatorForTest("self:9000", map[string]string{
		clusterID: strings.TrimPrefix(upstream.URL, "http://"),
	}))
	proxy := NewProxyHandler(hub, slog.Default())

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/clusters/"+clusterID+"/k8s/api/v1/pods?watch=true", nil)
	w := &flushSignalWriter{header: http.Header{}, flushed: make(chan struct{}, 8)}

	done := make(chan bool, 1)
	go func() {
		done <- proxy.forwardToOwnerPod(w, req, clusterID)
	}()

	// The first event must reach the client (be flushed) before the owner sends
	// the second — proving per-chunk flushing rather than end-of-stream buffering.
	select {
	case <-w.flushed:
	case <-time.After(3 * time.Second):
		close(proceed)
		t.Fatal("first watch event was not flushed to the client — cross-pod watch is buffered")
	}
	close(proceed)

	select {
	case forwarded := <-done:
		if !forwarded {
			t.Fatal("expected request to be forwarded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("forwardToOwnerPod did not complete")
	}

	body := w.body()
	if !strings.Contains(body, `"ADDED"`) || !strings.Contains(body, `"MODIFIED"`) {
		t.Fatalf("expected both events in body, got: %s", body)
	}
}
