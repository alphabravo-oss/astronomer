package monitoring

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type clusterRequesterFunc func(context.Context, string, string, string, []byte, map[string]string) (*protocol.K8sResponsePayload, error)

func (f clusterRequesterFunc) Do(ctx context.Context, c, m, p string, b []byte, h map[string]string) (*protocol.K8sResponsePayload, error) {
	return f(ctx, c, m, p, b, h)
}

func TestClusterClientUsesOnlyOwningClusterService(t *testing.T) {
	const id = "77777777-7777-4777-8777-777777777777"
	var seen []string
	requester := clusterRequesterFunc(func(_ context.Context, c, m, p string, _ []byte, h map[string]string) (*protocol.K8sResponsePayload, error) {
		if c != id || m != http.MethodGet || h["Authorization"] != "" {
			t.Fatalf("unexpected target or credentials: %s %s", c, m)
		}
		seen = append(seen, p)
		body := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"7"]}]}}`
		if strings.Contains(p, "?labelSelector=") {
			body = `{"items":[{"metadata":{"name":"demo-prometheus"},"spec":{"ports":[{"port":9090}]}}]}`
		} else if !strings.HasPrefix(p, "/api/v1/namespaces/monitoring/services/http:demo-prometheus:9090/proxy/") {
			t.Fatalf("escaped service target: %s", p)
		}
		if strings.Contains(p, "query_range") {
			body = `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1,"7"]]}]}}`
		}
		return &protocol.K8sResponsePayload{StatusCode: 200, Body: base64.StdEncoding.EncodeToString([]byte(body))}, nil
	})
	c, err := NewClusterClient(context.Background(), requester, id, "monitoring", "demo")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.QueryScalar(context.Background(), `sum(up{job="a&b"})`)
	if err != nil || got != 7 {
		t.Fatalf("scalar=%v err=%v", got, err)
	}
	u, _ := url.Parse(seen[1])
	if u.Query().Get("query") != `sum(up{job="a&b"})` {
		t.Fatal("query changed")
	}
	points, err := c.QueryRange(context.Background(), "up", time.Unix(1, 0), time.Unix(2, 0), time.Second)
	if err != nil || len(points) != 1 {
		t.Fatalf("range=%v err=%v", points, err)
	}
	if err := c.HealthCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestClusterClientFailsClosed(t *testing.T) {
	requester := clusterRequesterFunc(func(context.Context, string, string, string, []byte, map[string]string) (*protocol.K8sResponsePayload, error) {
		return nil, fmt.Errorf("disconnected")
	})
	if _, err := NewClusterClient(context.Background(), requester, "77777777-7777-4777-8777-777777777777", "monitoring", "demo"); err == nil {
		t.Fatal("disconnected cluster accepted")
	}
	tr := &clusterPrometheusTransport{requester: requester}
	for _, target := range []string{"http://169.254.169.254/api/v1/query", "http://cluster-prometheus/api/v1/namespaces", "http://cluster-prometheus/../secrets"} {
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		if _, err := tr.RoundTrip(req); err == nil {
			t.Fatalf("accepted %s", target)
		}
	}
}
