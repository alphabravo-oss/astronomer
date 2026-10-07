package kuberequests

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

func TestRepeatedConfigAndPriorRetryWrapperCountUnderlyingAttempts(t *testing.T) {
	attempts := 0
	order := []string{}
	original := &rest.Config{Host: "https://kubernetes.test", QPS: 7, Burst: 9, RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter(), Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		order = append(order, "transport")
		if r.Context().Value("retained") != "value" {
			t.Error("context value lost")
		}
		if attempts == 1 {
			return response(503, `{}`), nil
		}
		return response(200, `{}`), nil
	})}
	original.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return roundTripFunc(func(r *http.Request) (*http.Response, error) { order = append(order, "inner"); return rt.RoundTrip(r) })
	})
	original.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return roundTripFunc(func(r *http.Request) (*http.Response, error) {
			order = append(order, "retry")
			resp, err := rt.RoundTrip(r)
			if err == nil && resp.StatusCode == 503 {
				resp.Body.Close()
				return rt.RoundTrip(r)
			}
			return resp, err
		})
	})
	beforeSuccess := counterValue(DeliveryInventory, "list", "pods", "2xx")
	beforeError := counterValue(DeliveryInventory, "list", "pods", "5xx")
	cfg := DefaultConsumerConfig(Config(Config(original)), DeliveryInventory)
	if cfg == original || cfg.QPS != 7 || cfg.Burst != 9 || cfg.RateLimiter != original.RateLimiter {
		t.Fatal("config/rate behavior changed")
	}
	client, err := rest.HTTPClientFor(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), "retained", "value"))
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", "https://kubernetes.test/api/v1/pods", nil)
	resp, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if attempts != 2 || counterValue(DeliveryInventory, "list", "pods", "2xx")-beforeSuccess != 1 || counterValue(DeliveryInventory, "list", "pods", "5xx")-beforeError != 1 {
		t.Fatal("retry attempts double counted or missing")
	}
	if !reflect.DeepEqual(order, []string{"retry", "inner", "transport", "inner", "transport"}) {
		t.Fatalf("prior wrapper order changed: %v", order)
	}
	if request.Context() != ctx || request.Context().Value(spanKey{}) != nil || request.Context().Value(consumerKey{}) != nil {
		t.Fatal("original request mutated")
	}
}
func TestExplicitConsumerOverridesDiscoveryDefault(t *testing.T) {
	original := &rest.Config{Host: "https://kubernetes.test", Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { return response(200, `{}`), nil })}
	client, err := rest.HTTPClientFor(DefaultConsumerConfig(Config(original), DeliveryInventory))
	if err != nil {
		t.Fatal(err)
	}
	before := counterValue(SharedObservation, "get", "pods", "2xx")
	req, _ := http.NewRequestWithContext(WithConsumer(context.Background(), SharedObservation), "GET", "https://kubernetes.test/api/v1/namespaces/private/pods/private", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if counterValue(SharedObservation, "get", "pods", "2xx")-before != 1 {
		t.Fatal("default overrode explicit context")
	}
}
func TestTransportErrorAndCanceledContextPreserved(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	original := &rest.Config{Host: "https://kubernetes.test", Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Context().Err() != context.Canceled {
			t.Error("cancellation lost")
		}
		return nil, errors.New("transport")
	})}
	rt, err := rest.TransportFor(Config(original))
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://kubernetes.test/api/v1/pods", nil)
	before := counterValue(Other, "list", "pods", "transport_error")
	if _, err = rt.RoundTrip(req); err == nil {
		t.Fatal("transport error hidden")
	}
	if counterValue(Other, "list", "pods", "transport_error")-before != 1 {
		t.Fatal("transport failure not counted")
	}
}
func TestTokenFileAndExistingAuthWrapperPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("first-test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	expected := "Bearer first-test-token"
	original := &rest.Config{Host: "https://kubernetes.test", BearerTokenFile: path, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != expected {
			t.Error("client-go token-file auth not preserved")
		}
		return response(200, `{}`), nil
	})}
	copied := Config(original)
	if original.BearerTokenFile != path || original.WrapTransport != nil || copied.BearerTokenFile != path {
		t.Fatal("original config modified")
	}
	for _, token := range []string{"first-test-token", "second-test-token"} {
		if err := os.WriteFile(path, []byte(token), 0600); err != nil {
			t.Fatal(err)
		}
		expected = "Bearer " + token
		client, err := rest.HTTPClientFor(copied)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Get("https://kubernetes.test/api/v1/pods")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
}
