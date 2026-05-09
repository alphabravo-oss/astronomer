package remoteproxy_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rancher/remotedialer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/alphabravocompany/astronomer-go/internal/handler/remoteproxy"
)

// fakeTunnel implements remoteproxy.TunnelDialer in-memory: every dial is
// short-circuited to a local httptest.Server that pretends to be the
// in-cluster API server. This is the smallest possible end-to-end test of
// the K8sClient plumbing — it does NOT run the remotedialer protocol, but it
// does exercise rest.Config -> http.Transport -> Dial -> response parsing.
type fakeTunnel struct {
	target  string // host:port of the httptest.Server
	hasSess bool
}

func (f *fakeTunnel) HasSession(_ string) bool { return f.hasSess }

func (f *fakeTunnel) DialerFor(_ string) remotedialer.Dialer {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, f.target)
	}
}

func TestK8sClient_ListsPodsThroughDialer(t *testing.T) {
	// Stand up a fake API server that answers
	// GET /api/v1/namespaces/{ns}/pods with a minimal PodList.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/pods") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind":       "PodList",
			"apiVersion": "v1",
			"metadata":   map[string]any{},
			"items": []map[string]any{
				{
					"metadata": map[string]any{"name": "demo-pod", "namespace": "default"},
					"status":   map[string]any{"phase": "Running"},
				},
			},
		})
	}))
	defer srv.Close()

	// Strip the scheme so we have a host:port for net.Dial.
	target := strings.TrimPrefix(srv.URL, "https://")
	// httptest's TLS server uses an ephemeral cert; the K8sClient's transport
	// already does Insecure=true so this works without us supplying a cert.
	_ = tls.VersionTLS12

	tunnel := &fakeTunnel{target: target, hasSess: true}

	client, err := remoteproxy.K8sClient(tunnel, "00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatalf("K8sClient: %v", err)
	}

	pods, err := client.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) != 1 || pods.Items[0].Name != "demo-pod" {
		t.Fatalf("unexpected pod list: %+v", pods.Items)
	}
}

func TestK8sClient_AgentOffline(t *testing.T) {
	tunnel := &fakeTunnel{hasSess: false}
	if _, err := remoteproxy.K8sClient(tunnel, "abc"); err == nil {
		t.Fatal("expected error when agent has no session")
	}
}

func TestK8sClient_NilServer(t *testing.T) {
	if _, err := remoteproxy.K8sClient(nil, "abc"); err == nil {
		t.Fatal("expected error for nil tunnel")
	}
}

func TestK8sClient_EmptyClusterID(t *testing.T) {
	tunnel := &fakeTunnel{hasSess: true}
	if _, err := remoteproxy.K8sClient(tunnel, ""); err == nil {
		t.Fatal("expected error for empty cluster id")
	}
}
