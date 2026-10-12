package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestInternalPeerForwardingRejectsRedirects(t *testing.T) {
	for _, role := range []string{"k8s", "helm"} {
		t.Run(role, func(t *testing.T) {
			for _, code := range []int{301, 302, 303, 307, 308} {
				t.Run(http.StatusText(code), func(t *testing.T) {
					var diverted, received, authenticated atomic.Int32
					sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						diverted.Add(1)
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"success":true,"status_code":200}`)
					}))
					defer sink.Close()
					peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						received.Add(1)
						body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
						if err == nil && len(body) > 0 && r.Header.Get(tunnel.InternalSignatureHeader) != "" {
							authenticated.Add(1)
						}
						w.Header().Set("Location", sink.URL+"/diverted")
						w.WriteHeader(code)
					}))
					defer peer.Close()
					hub := tunnel.NewHub(nil)
					hub.SetLocator(tunnel.NewFakeLocatorForTest("self:8000", map[string]string{
						"c1": strings.TrimPrefix(peer.URL, "http://"),
					}))
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					var err error
					if role == "k8s" {
						requester := NewTunnelK8sRequester(hub)
						requester.SetInternalPSK("fixture-peer-key")
						_, err = requester.Do(ctx, "c1", http.MethodPost, "/api/v1/namespaces/default/secrets", []byte(`{"fixture":"private-payload"}`), nil)
					} else {
						requester := NewTunnelHelmRequester(hub)
						requester.SetInternalPSK("fixture-peer-key")
						_, err = requester.Do(ctx, "c1", protocol.MsgHelmInstall, protocol.HelmRequestPayload{ReleaseName: "private-payload", Namespace: "default"})
					}
					if got := diverted.Load(); got != 0 {
						t.Fatalf("redirect destination received %d requests", got)
					}
					if received.Load() != 1 || authenticated.Load() != 1 {
						t.Fatalf("original peer did not receive exactly one signed payload: requests=%d signed=%d", received.Load(), authenticated.Load())
					}
					if err == nil || !strings.Contains(err.Error(), "cross-pod redirects are not permitted") {
						t.Fatalf("redirect was not rejected explicitly: %v", err)
					}
				})
			}
		})
	}
}
