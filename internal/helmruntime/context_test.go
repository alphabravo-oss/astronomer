package helmruntime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type requestGetterFixture struct{ config *rest.Config }

func (fixture requestGetterFixture) ToRESTConfig() (*rest.Config, error) { return fixture.config, nil }
func (requestGetterFixture) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return nil, errors.New("unbound discovery must not be used")
}
func (requestGetterFixture) ToRESTMapper() (meta.RESTMapper, error) {
	return nil, errors.New("unbound mapper must not be used")
}
func (requestGetterFixture) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return clientcmd.NewDefaultClientConfig(clientcmdapi.Config{}, &clientcmd.ConfigOverrides{})
}

type roundTripFunction func(*http.Request) (*http.Response, error)

func (function roundTripFunction) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type requestValueKey int

func TestHelmOwnedRequestPreservesTransportSDKContextAndOwnerValues(t *testing.T) {
	var wrapped atomic.Int32
	owner := context.WithValue(context.Background(), requestValueKey(1), "synthetic owner correlation")
	source := &rest.Config{Host: "https://example.invalid", WrapTransport: func(next http.RoundTripper) http.RoundTripper { wrapped.Add(1); return next }}
	getter, err := RequestClientGetter(owner, requestGetterFixture{config: source})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := getter.ToRESTConfig()
	if err != nil {
		t.Fatal(err)
	}
	sdk, stop := context.WithTimeout(context.WithValue(context.Background(), requestValueKey(2), "synthetic SDK correlation"), time.Second)
	defer stop()
	seen := false
	transport := cfg.WrapTransport(roundTripFunction(func(request *http.Request) (*http.Response, error) {
		seen = true
		if request.Context().Value(requestValueKey(1)) != "synthetic owner correlation" || request.Context().Value(requestValueKey(2)) != "synthetic SDK correlation" {
			t.Error("owner/SDK values lost")
		}
		deadline, ok := request.Context().Deadline()
		expected, _ := sdk.Deadline()
		if !ok || !deadline.Equal(expected) {
			t.Error("SDK deadline lost")
		}
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	}))
	request, _ := http.NewRequestWithContext(sdk, http.MethodGet, "https://example.invalid", nil)
	response, err := transport.RoundTrip(request)
	if err != nil || !seen || wrapped.Load() != 1 {
		t.Fatal("existing transport wrapper bypassed")
	}
	_ = response.Body.Close()
	if source.Timeout != 0 {
		t.Fatal("shared base REST config mutated")
	}
	raw, err := getter.ToRawKubeConfigLoader().ClientConfig()
	if err != nil || raw.WrapTransport == nil {
		t.Fatal("raw client loader bypasses bound transport")
	}
}
func TestHelmOwnedRequestCancelsRealStreamingBodyAfterHeaders(t *testing.T) {
	observed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(200)
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
		close(observed)
	}))
	defer server.Close()
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	getter, err := RequestClientGetter(owner, requestGetterFixture{config: &rest.Config{Host: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := getter.ToRESTConfig()
	if err != nil {
		t.Fatal(err)
	}
	client, err := rest.HTTPClientFor(cfg)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	completed := make(chan error, 1)
	go func() { _, err := io.ReadAll(response.Body); completed <- err }()
	cancel()
	select {
	case err = <-completed:
		if err == nil {
			t.Fatal("stream completed without cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owned stream survived cancellation")
	}
	select {
	case <-observed:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not observe owner cancellation")
	}
}
func TestHelmDiscoveryUsesOwnedGetterAndJoinsCancellation(t *testing.T) {
	entered, stopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(entered)
		<-request.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	getter, err := RequestClientGetter(owner, requestGetterFixture{config: &rest.Config{Host: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	client, err := getter.ToDiscoveryClient()
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := client.ServerVersion(); result <- err }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("owned discovery did not reach server")
	}
	cancel()
	select {
	case err = <-result:
		if err == nil {
			t.Fatal("cancelled discovery succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("discovery cancellation did not join")
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("discovery transport not cancelled")
	}
}
func TestHelmOwnedGetterRejectsAbsentOrExpiredContext(t *testing.T) {
	if _, err := RequestClientGetter(nil, requestGetterFixture{}); err == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil owner admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RequestClientGetter(ctx, requestGetterFixture{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled owner admitted")
	}
}

func TestHelmOperationDeadlinePreservesEarlierCallerAndRejectsInvalidScope(t *testing.T) {
	parent, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	owned, cancel, err := OperationContext(parent, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	want, _ := parent.Deadline()
	got, ok := owned.Deadline()
	if !ok || !got.Equal(want) {
		t.Fatal("earlier caller deadline lost")
	}
	finite, finish, err := OperationContext(context.Background(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	if _, ok := finite.Deadline(); !ok {
		t.Fatal("operation is not bounded")
	}
	for _, timeout := range []time.Duration{0, -time.Second} {
		if _, _, err := OperationContext(context.Background(), timeout); err == nil {
			t.Fatal("invalid timeout admitted")
		}
	}
	if _, _, err := OperationContext(nil, time.Minute); err == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil context admitted")
	}
	stop()
	if _, _, err := OperationContext(parent, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled caller identity lost")
	}
}

func TestHelmHTTPClientBindsBackgroundRequestWithoutMutatingOriginalPolicy(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	base := &http.Client{Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: roundTripFunction(func(request *http.Request) (*http.Response, error) {
		close(entered)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	bound, err := HTTPClient(owner, base)
	if err != nil {
		t.Fatal(err)
	}
	if bound == base || bound.Timeout != base.Timeout || bound.CheckRedirect == nil {
		t.Fatal("HTTP policy changed")
	}
	result := make(chan error, 1)
	go func() { _, err := bound.Get("https://example.invalid"); result <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request not entered")
	}
	cancel()
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("background request escaped owner cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("owned HTTP client did not join")
	}
	if _, ok := base.Transport.(roundTripFunction); !ok {
		t.Fatal("shared client mutated")
	}
}
