package delivery

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/agent/kuberequests"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

type requestTestTransport func(*http.Request) (*http.Response, error)

func (f requestTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func requestTestResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func deliveryRequestCount(consumer, operation string) float64 {
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
			if labels["consumer"] == consumer && (operation == "" || labels["operation"] == operation) {
				total += m.GetCounter().GetValue()
			}
		}
	}
	return total
}
func TestRuntimeObserveTagsReadsWithoutRetaggingExecutor(t *testing.T) {
	cfg := kuberequests.Config(&rest.Config{Host: "https://kubernetes.test", Transport: requestTestTransport(func(r *http.Request) (*http.Response, error) {
		kind, version := "GitRepository", "source.toolkit.fluxcd.io/v1"
		if strings.Contains(r.URL.Path, "kustomizations") {
			kind, version = "Kustomization", "kustomize.toolkit.fluxcd.io/v1"
		}
		return requestTestResponse(fmt.Sprintf(`{"apiVersion":%q,"kind":%q,"metadata":{"name":"fixture"}}`, version, kind)), nil
	})})
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewExecutor(client)
	if err != nil {
		t.Fatal(err)
	}
	accepted := AcceptedAssignment{Objects: []ObjectIdentity{{Group: "source.toolkit.fluxcd.io", Version: "v1", Kind: "GitRepository", Name: "fixture", Namespace: "scope"}, {Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Kind: "Kustomization", Name: "fixture", Namespace: "scope"}}}
	runtime := &Runtime{executor: executor}
	before := deliveryRequestCount("delivery_assignment_observation", "get")
	if _, _, err = runtime.observe(context.Background(), accepted); err != nil {
		t.Fatal(err)
	}
	if deliveryRequestCount("delivery_assignment_observation", "get")-before != 2 {
		t.Fatal("runtime source/reconciler GET attribution missing")
	}
	before = deliveryRequestCount("other", "get")
	if _, err = executor.Get(context.Background(), accepted.Objects[0]); err != nil {
		t.Fatal(err)
	}
	if deliveryRequestCount("other", "get")-before != 1 {
		t.Fatal("Executor.Get globally retagged")
	}
}
func TestLegacyAndModernProbeAttributeInventoryRequests(t *testing.T) {
	calls := 0
	cfg := kuberequests.Config(&rest.Config{Host: "https://kubernetes.test", Transport: requestTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		path := r.URL.Path
		if path == "/version" {
			return requestTestResponse(`{"gitVersion":"v1.35.2"}`), nil
		}
		if path == "/apis" {
			return requestTestResponse(`{"kind":"APIGroupList","apiVersion":"v1","groups":[]}`), nil
		}
		if path == "/api" {
			return requestTestResponse(`{"kind":"APIVersions","apiVersion":"v1","versions":["v1"]}`), nil
		}
		if !strings.Contains(path, "/namespaces/") && !strings.HasSuffix(path, "deployments") {
			return requestTestResponse(fmt.Sprintf(`{"kind":"APIResourceList","apiVersion":"v1","groupVersion":%q,"resources":[]}`, strings.TrimPrefix(path, "/apis/"))), nil
		}
		if strings.HasSuffix(path, "deployments") {
			return requestTestResponse(`{"kind":"DeploymentList","apiVersion":"apps/v1","items":[]}`), nil
		}
		resp := requestTestResponse(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
		resp.StatusCode = 404
		return resp, nil
	})})
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := NewClusterProbeForConfig(client, cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	before := deliveryRequestCount("delivery_inventory", "")
	beforeDiscovery := deliveryRequestCount("delivery_inventory", "discovery")
	_, _, _ = probe.Inspect(context.Background())
	if calls == 0 || deliveryRequestCount("delivery_inventory", "")-before != float64(calls) || deliveryRequestCount("delivery_inventory", "discovery") <= beforeDiscovery {
		t.Fatal("legacy discovery or typed requests lost inventory attribution")
	}
	_, _, source := sharedProbeFixture(t)
	probe.WithObservationSource(source)
	before = deliveryRequestCount("delivery_inventory", "")
	calls = 0
	_, _, _ = probe.InspectObserved(context.Background())
	if calls == 0 || deliveryRequestCount("delivery_inventory", "")-before != float64(calls) {
		t.Fatal("modern refresh requests lost attribution")
	}
}
func TestAssignmentCacheCallbacksTagListAndWatch(t *testing.T) {
	cfg := kuberequests.Config(&rest.Config{Host: "https://kubernetes.test", Transport: requestTestTransport(func(r *http.Request) (*http.Response, error) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		resource := parts[len(parts)-1]
		kind := ""
		for _, gvk := range assignmentGVKs {
			if deliveryResources[gvk].resource.Resource == resource {
				kind = gvk.Kind
			}
		}
		if kind == "" {
			return nil, fmt.Errorf("unknown fixture kind")
		}
		if r.URL.Query().Get("labelSelector") != ManagedByLabel+"="+ManagedByValue {
			t.Error("ownership selector changed")
		}
		if r.URL.Query().Get("watch") == "true" {
			reader, writer := io.Pipe()
			go func() { <-r.Context().Done(); writer.Close() }()
			resp := requestTestResponse("")
			resp.Body = reader
			return resp, nil
		}
		return requestTestResponse(fmt.Sprintf(`{"apiVersion":%q,"kind":%q,"metadata":{"resourceVersion":"1"},"items":[]}`, parts[1]+"/"+parts[2], kind+"List")), nil
	})})
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewAssignmentCache(client, AssignmentCacheOptions{})
	if err != nil {
		t.Fatal(err)
	}
	beforeList := deliveryRequestCount("delivery_assignment_observation", "list")
	beforeWatch := deliveryRequestCount("delivery_assignment_observation", "watch")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cache.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("assignment watchers failed to stop")
		}
	}()
	err = wait.PollUntilContextTimeout(ctx, 5*time.Millisecond, 5*time.Second, true, func(context.Context) (bool, error) {
		return deliveryRequestCount("delivery_assignment_observation", "list")-beforeList == 5 && deliveryRequestCount("delivery_assignment_observation", "watch")-beforeWatch == 5, nil
	})
	if err != nil {
		t.Fatal("five watcher callbacks not attributed", err)
	}
}

func TestDedicatedDiscoveryPreservesExistingRateLimiter(t *testing.T) {
	for _, original := range []*rest.Config{
		{Host: "https://kubernetes.test", Burst: 9},
		{Host: "https://kubernetes.test", QPS: 7, Burst: 9, Timeout: 5 * time.Second},
		{Host: "https://kubernetes.test", RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()},
	} {
		cfg := kuberequests.Config(original)
		client, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		prior := client.Discovery().RESTClient().GetRateLimiter()
		if prior == nil {
			t.Fatal("clientset did not synthesize limiter")
		}
		probe, err := NewClusterProbeForConfig(client, cfg, false)
		if err != nil {
			t.Fatal(err)
		}
		if probe.discovery.RESTClient().GetRateLimiter() != prior {
			t.Fatal("dedicated discovery changed shared limiter identity")
		}
		oldREST := client.Discovery().RESTClient().(*rest.RESTClient)
		newREST := probe.discovery.RESTClient().(*rest.RESTClient)
		if oldREST.Client.Timeout != newREST.Client.Timeout {
			t.Fatal("dedicated discovery changed HTTP timeout")
		}
	}
	if _, err := NewClusterProbeForConfig(fake.NewClientset(), &rest.Config{Host: "https://kubernetes.test"}, false); err != nil {
		t.Fatal("fake discovery nil RESTClient fallback failed", err)
	}
}
