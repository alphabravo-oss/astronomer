package helmruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func TestHelmRepositoryPolicyDoesNotTrustChartURLOrLoopback(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	for _, policy := range []string{"", `{"restricted":true,"origins":[{"url":"` + server.URL + `","cidrs":["127.0.0.0/8"]}]}`} {
		client, err := RepositoryClient(context.Background(), Config{RepositoryDestinationPolicy: policy}, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Get(server.URL + "/index.yaml")
		client.CloseIdleConnections()
		if err == nil {
			t.Fatal("request selected its own trusted loopback destination")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("blocked repository reached listener")
	}
}

func TestHelmRepositoryPolicyPreservesExactConfiguredPrivateAuthority(t *testing.T) {
	policy, err := httpclient.ParseDestinationPolicy(`{"origins":[{"url":"https://10.42.0.12:8443","cidrs":["10.42.0.12/32"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.ValidateURL(context.Background(), "https://10.42.0.12:8443/chart.tgz"); err != nil {
		t.Fatal("operator-pinned private chart authority rejected")
	}
	for _, address := range []string{"https://10.42.0.13:8443/chart.tgz", "https://10.42.0.12:8444/chart.tgz", "http://169.254.169.254/latest/meta-data"} {
		if err := policy.ValidateURL(context.Background(), address); err == nil {
			t.Fatal("chart URL broadened exact operator authority")
		}
	}
	// This checks authority and default denial. A successful private transfer
	// still needs the separately owned endpoint qualification.
}

func TestHelmRepositoryPolicyRejectsMalformedOperatorConfiguration(t *testing.T) {
	for _, value := range []string{`{"origins":[],"origins":[]}`, `{"restricted":true}`, `{"unexpected":true}`} {
		if _, err := RepositoryClient(context.Background(), Config{RepositoryDestinationPolicy: value}, nil); err == nil {
			t.Fatal("malformed private authority admitted")
		}
	}
}

func TestHelmRepositoryProxyRequiresIndependentOperatorAuthority(t *testing.T) {
	for _, proxy := range []string{"http://proxy.example:3128", "https://proxy.example/connect", "https://proxy.example?"} {
		if _, err := RepositoryClient(context.Background(), Config{RepositoryProxyURL: proxy}, nil); err == nil {
			t.Fatal("unsupported operator proxy admitted")
		}
	}
	// A configured origin cannot turn a loopback proxy into approved access.
	for _, policy := range []string{"", `{"restricted":true,"origins":[{"url":"https://127.0.0.1:8443","cidrs":["127.0.0.0/8"]}]}`} {
		if _, err := RepositoryClient(context.Background(), Config{RepositoryProxyURL: "https://127.0.0.1:8443", RepositoryDestinationPolicy: policy}, nil); err == nil {
			t.Fatal("configured proxy granted loopback access")
		}
	}
	policy, err := httpclient.ParseDestinationPolicy(`{"restricted":true,"origins":[{"url":"https://10.42.0.12:8443","cidrs":["10.42.0.12/32"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.ValidateURL(context.Background(), "https://10.42.0.13:8443/chart.tgz"); err == nil {
		t.Fatal("a proxy origin granted authority to an unrelated repository")
	}
}

func TestHelmRepositoryTypedProxyAllowsOperatorAuthAndRejectsAmbiguousInputs(t *testing.T) {
	client, err := RepositoryClient(context.Background(), Config{RepositoryProxyRouting: httpclient.ProxyRouting{URL: "https://operator:fixture@proxy.example"}}, nil)
	if err != nil || client == nil {
		t.Fatal("typed operator proxy authentication shape was rejected")
	}
	client.CloseIdleConnections()
	if _, err := RepositoryClient(context.Background(), Config{RepositoryProxyURL: "https://legacy.example", RepositoryProxyRouting: httpclient.ProxyRouting{HTTPSURL: "https://typed.example"}}, nil); err == nil {
		t.Fatal("ambiguous legacy/typed repository proxy input admitted")
	}
}
