package kuberequests

import (
	"context"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	dto "github.com/prometheus/client_model/go"
	"io"
	"net/http"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func TestClientGoCountsTaggedList(t *testing.T) {
	cfg := &rest.Config{Host: "https://kubernetes.test", Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(200, `{"apiVersion":"v1","kind":"PodList","metadata":{},"items":[]}`), nil
	})}
	client, err := kubernetes.NewForConfig(Config(cfg))
	if err != nil {
		t.Fatal(err)
	}
	before := counterValue(SharedObservation, "list", "pods", "2xx")
	if _, err = client.CoreV1().Pods("").List(WithConsumer(context.Background(), SharedObservation), metav1.ListOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := counterValue(SharedObservation, "list", "pods", "2xx") - before; got != 1 {
		t.Fatalf("got %v attempts", got)
	}
}

func counterValue(consumer Consumer, operation, resource, outcome string) float64 {
	m := &dto.Metric{}
	_ = requests.WithLabelValues(observability.MetricValues(consumer.label(), operation, resource, outcome)...).Write(m)
	return m.GetCounter().GetValue()
}

func TestInstrumentationSentinelDescribesInstalledSchema(t *testing.T) {
	cfg := Config(&rest.Config{Host: "https://kubernetes.test"})
	if _, err := rest.HTTPClientFor(cfg); err != nil {
		t.Fatal(err)
	}
	m := &dto.Metric{}
	if err := instrumentation.WithLabelValues(observability.MetricValues("v1")...).Write(m); err != nil {
		t.Fatal(err)
	}
	if m.GetGauge().GetValue() != 1 || len(m.Label) != 2 {
		t.Fatal("instrumentation sentinel missing or unbounded labels")
	}
	for _, label := range m.Label {
		if label.GetName() != "schema" && label.GetName() != "astronomer_instance_id" {
			t.Fatal("unexpected sentinel label")
		}
		if label.GetName() == "schema" && label.GetValue() != "v1" {
			t.Fatal("schema changed")
		}
	}
}
