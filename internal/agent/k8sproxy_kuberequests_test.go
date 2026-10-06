package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
)

func agentRequestCount(consumer, operation, resource string) float64 {
	families, _ := prometheus.DefaultGatherer.Gather()
	total := 0.0
	for _, f := range families {
		if f.GetName() != "astronomer_agent_kubernetes_requests_total" {
			continue
		}
		for _, m := range f.Metric {
			labels := map[string]string{}
			for _, l := range m.Label {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["consumer"] == consumer && labels["operation"] == operation && (resource == "" || labels["resource"] == resource) {
				total += m.GetCounter().GetValue()
			}
		}
	}
	return total
}
func TestProxyConfigSharesRequestInstrumentation(t *testing.T) {
	original := &rest.Config{Host: "https://kubernetes.test", Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"fixture"}}`))}, nil
	})}
	proxy, err := NewK8sProxyWithConfig(original, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if original.WrapTransport != nil || proxy.RESTConfig() == original {
		t.Fatal("caller config changed")
	}
	before := agentRequestCount("other", "get", "pods")
	if _, err = proxy.Client().CoreV1().Pods("scope").Get(context.Background(), "fixture", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	dc, err := dynamic.NewForConfig(proxy.RESTConfig())
	if err != nil {
		t.Fatal(err)
	}
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	if _, err = dc.Resource(gvr).Namespace("scope").Get(context.Background(), "fixture", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	mc, err := metadata.NewForConfig(proxy.RESTConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mc.Resource(gvr).Namespace("scope").Get(context.Background(), "fixture", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	if agentRequestCount("other", "get", "pods")-before != 3 {
		t.Fatal("typed/dynamic/metadata config reuse lost or duplicated attempts")
	}
}
func TestSharedFactoryCallbacksTagActualListWatchRequests(t *testing.T) {
	kinds := map[string]string{"deployments": "Deployment", "statefulsets": "StatefulSet", "daemonsets": "DaemonSet", "pods": "Pod", "persistentvolumeclaims": "PersistentVolumeClaim", "storageclasses": "StorageClass"}
	cfg := &rest.Config{Host: "https://kubernetes.test", Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		resource := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		kind := kinds[resource]
		if kind == "" {
			t.Errorf("unexpected tracked kind")
			return nil, fmt.Errorf("unknown test resource")
		}
		var body io.ReadCloser
		if r.URL.Query().Get("watch") == "true" {
			reader, writer := io.Pipe()
			body = reader
			go func() { <-r.Context().Done(); writer.Close() }()
		} else {
			version := "v1"
			if strings.Contains(r.URL.Path, "/apps/") {
				version = "apps/v1"
			}
			if resource == "storageclasses" {
				version = "storage.k8s.io/v1"
			}
			body = io.NopCloser(strings.NewReader(fmt.Sprintf(`{"apiVersion":%q,"kind":%q,"metadata":{"resourceVersion":"1"},"items":[]}`, version, kind+"List")))
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: body}, nil
	})}
	proxy, err := NewK8sProxyWithConfig(cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	subscriber := NewStateSubscriber(proxy.Client(), nil, slog.Default())
	factory := informers.NewSharedInformerFactory(proxy.Client(), 0)
	subscriber.registerObservations(factory)
	beforeList := agentRequestCount("shared_observation", "list", "")
	beforeWatch := agentRequestCount("shared_observation", "watch", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); subscriber.shutdownObservations(factory) }()
	factory.Start(ctx.Done())
	err = wait.PollUntilContextTimeout(ctx, 5*time.Millisecond, 5*time.Second, true, func(context.Context) (bool, error) {
		return agentRequestCount("shared_observation", "list", "")-beforeList == 6 && agentRequestCount("shared_observation", "watch", "")-beforeWatch == 6, nil
	})
	if err != nil {
		t.Fatal("factory callback-local request attribution missing", err)
	}
}
