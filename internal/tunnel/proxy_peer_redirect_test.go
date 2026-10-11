package tunnel

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyPeerForwardingDoesNotFollowRedirects(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var diverted, received, authenticated atomic.Int32
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				diverted.Add(1)
				_, _ = io.WriteString(w, `{"diverted":true}`)
			}))
			defer sink.Close()
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received.Add(1)
				if r.Header.Get("Authorization") == "Bearer fixture-caller" && r.Header.Get("Cookie") == "session=fixture-session" {
					authenticated.Add(1)
				}
				w.Header().Set("Location", sink.URL+"/diverted")
				w.WriteHeader(code)
			}))
			defer peer.Close()
			hub := NewHub(slog.Default())
			hub.SetLocator(NewFakeLocatorForTest("self:8000", map[string]string{
				"c1": strings.TrimPrefix(peer.URL, "http://"),
			}))
			proxy := NewProxyHandler(hub, slog.Default())
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/c1/k8s/api/v1/pods", nil).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer fixture-caller")
			req.Header.Set("Cookie", "session=fixture-session")
			w := httptest.NewRecorder()
			forwarded := proxy.forwardToOwnerPod(w, req, "c1")
			if got := diverted.Load(); got != 0 {
				t.Fatalf("redirect destination received %d requests", got)
			}
			if received.Load() != 1 || authenticated.Load() != 1 {
				t.Fatalf("original peer did not receive exactly one authenticated request: requests=%d authenticated=%d", received.Load(), authenticated.Load())
			}
			if forwarded {
				// A streaming request cannot be replayed for 307/308. Preserve
				// the original downstream response without following its URL.
				if w.Code != code || w.Header().Get("Location") != sink.URL+"/diverted" || w.Body.Len() != 0 {
					t.Fatalf("original redirect response changed: status=%d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
				}
			} else if w.Header().Get("Location") != "" || w.Body.Len() != 0 {
				t.Fatalf("failed forwarding wrote a response: headers=%v body=%q", w.Header(), w.Body.String())
			}
		})
	}
}
