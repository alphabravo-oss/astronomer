package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/agent"
	agentdelivery "github.com/alphabravocompany/astronomer-go/internal/agent/delivery"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

type localRequestTransport func(*http.Request) (*http.Response, error)

func (f localRequestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func localRequestCount() float64 {
	families, _ := prometheus.DefaultGatherer.Gather()
	n := 0.0
	for _, f := range families {
		if f.GetName() == "astronomer_agent_kubernetes_requests_total" {
			for _, m := range f.Metric {
				n += m.GetCounter().GetValue()
			}
		}
	}
	return n
}
func TestEmbeddedClientConfigAndProxyCountOnce(t *testing.T) {
	calls := 0
	original := &rest.Config{Host: "https://kubernetes.test", Transport: localRequestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body := `{"apiVersion":"v1","kind":"PodList","items":[]}`
		if r.URL.Path == "/version" {
			body = `{"gitVersion":"v1.35.0"}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	cfg, client, err := newLocalAgentKubernetesClient(original)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := agent.NewK8sProxyWithConfig(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := localRequestCount()
	if _, err = client.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = proxy.Client().CoreV1().Pods("").List(context.Background(), metav1.ListOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = agentdelivery.NewClusterProbeForConfig(client, cfg, false); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || localRequestCount()-before != 2 || cfg == original || original.WrapTransport != nil {
		t.Fatal("embedded/proxy composition changed caller or double counted")
	}
}
