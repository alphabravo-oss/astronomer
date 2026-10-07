package delivery

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

type discoveryRoundTrip func(*http.Request) (*http.Response, error)

func (f discoveryRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
func TestModernDiscoveryCancelsActualRESTTransport(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	transport := discoveryRoundTrip(func(request *http.Request) (*http.Response, error) {
		once.Do(func() { close(started) })
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	client, err := discovery.NewDiscoveryClientForConfigAndClient(&rest.Config{Host: "https://context-test.invalid"}, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	probe := &ClusterProbe{discovery: client}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan discoverySnapshot, 1)
	go func() { done <- probe.readDiscovery(ctx, true) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("REST request not started")
	}
	cancel()
	select {
	case result := <-done:
		if !errors.Is(result.versionError, context.Canceled) {
			t.Fatal("version request ignored cancellation", result.versionError)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery transport outlived context")
	}
}
