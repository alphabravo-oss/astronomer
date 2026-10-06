package kuberequests

import (
	"context"
	"net/http"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/pager"
)

func TestClientGoPaginatedListAndRetry(t *testing.T) {
	calls := 0
	cfg := Config(&rest.Config{Host: "https://kubernetes.test", Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			resp := response(429, `{"kind":"Status","apiVersion":"v1","status":"Failure","code":429}`)
			resp.Header.Set("Retry-After", "0")
			return resp, nil
		case 2:
			return response(200, `{"apiVersion":"v1","kind":"PodList","metadata":{"resourceVersion":"1","continue":"opaque-cursor"},"items":[]}`), nil
		default:
			if r.URL.Query().Get("continue") != "opaque-cursor" {
				t.Error("pagination cursor lost")
			}
			return response(200, `{"apiVersion":"v1","kind":"PodList","metadata":{"resourceVersion":"1"},"items":[]}`), nil
		}
	})})
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	beforeOK := counterValue(SharedObservation, "list", "pods", "2xx")
	beforeRetry := counterValue(SharedObservation, "list", "pods", "4xx")
	p := pager.New(func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
		return client.CoreV1().Pods("").List(ctx, o)
	})
	if _, _, err = p.List(WithConsumer(context.Background(), SharedObservation), metav1.ListOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || counterValue(SharedObservation, "list", "pods", "2xx")-beforeOK != 2 || counterValue(SharedObservation, "list", "pods", "4xx")-beforeRetry != 1 {
		t.Fatal("pagination/retry attempts incorrect")
	}
}
func TestClientGoWatchFramesDoNotCountRequests(t *testing.T) {
	cfg := Config(&rest.Config{Host: "https://kubernetes.test", Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(200, "{\"type\":\"ADDED\",\"object\":{\"apiVersion\":\"v1\",\"kind\":\"Pod\",\"metadata\":{\"name\":\"private\",\"resourceVersion\":\"1\"}}}\n{\"type\":\"DELETED\",\"object\":{\"apiVersion\":\"v1\",\"kind\":\"Pod\",\"metadata\":{\"name\":\"private\",\"resourceVersion\":\"2\"}}}\n"), nil
	})})
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	before := counterValue(SharedObservation, "watch", "pods", "2xx")
	stream, err := client.CoreV1().Pods("").Watch(WithConsumer(context.Background(), SharedObservation), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Stop()
	events := 0
	for range stream.ResultChan() {
		events++
	}
	if events != 2 || counterValue(SharedObservation, "watch", "pods", "2xx")-before != 1 {
		t.Fatal("watch frames counted as requests")
	}
}
func TestLegacyDiscoveryDefaultSurvivesContextDiscard(t *testing.T) {
	cfg := Config(&rest.Config{Host: "https://kubernetes.test", Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { return response(200, `{"gitVersion":"v1.35.0"}`), nil })})
	client, err := discovery.NewDiscoveryClientForConfig(DefaultConsumerConfig(cfg, DeliveryInventory))
	if err != nil {
		t.Fatal(err)
	}
	before := counterValue(DeliveryInventory, "discovery", "other", "2xx")
	if _, err = client.ServerVersion(); err != nil {
		t.Fatal(err)
	}
	if counterValue(DeliveryInventory, "discovery", "other", "2xx")-before != 1 {
		t.Fatal("legacy discovery attribution lost")
	}
}
func TestClassificationNeverLabelsIdentityOrSubresource(t *testing.T) {
	for _, test := range []struct{ method, path, op, resource string }{
		{"GET", "/api/v1/namespaces/private/pods/private?token=private", "get", "pods"},
		{"GET", "/api/v1/namespaces/private/pods/private/status", "other", "pods"},
		{"GET", "/apis/unknown.test/v1/pods", "other", "other"},
		{"GET", "/api/v1/secrets/private", "other", "other"},
		{"POST", "/api/v1/pods", "other", "other"},
		{"GET", "/proxy/private/api/v1/pods", "other", "other"},
		{"GET", "/api/v1/pods?watch=true", "watch", "pods"},
		{"GET", "/apis/apps/v1", "discovery", "other"},
		{"GET", "/api/v1/namespaces/private%2Fname/pods", "other", "other"},
	} {
		r, _ := http.NewRequest(test.method, "https://kubernetes.test"+test.path, nil)
		op, resource := classify(r)
		if op != test.op || resource != test.resource {
			t.Fatalf("classification got%s/%s want%s/%s", op, resource, test.op, test.resource)
		}
	}
}
