package tasks

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type catalogNetworkRoundTrip func(*http.Request) (*http.Response, error)

func (f catalogNetworkRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestScheduledCatalogFetchHasNoDisconnectedPublicFallback(t *testing.T) {
	deps := RuntimeDependencies{}
	cfg := &config.Config{DistributionDestinationPolicy: `{"restricted":true,"origins":[{"url":"http://10.42.1.7:8080","cidrs":["10.42.1.7/32"]}]}`}
	if err := ConfigureCatalogNetwork(&deps, cfg); err != nil {
		t.Fatal(err)
	}
	approved, fallback := 0, 0
	deps.CatalogHTTPClient = &http.Client{Transport: catalogNetworkRoundTrip(func(r *http.Request) (*http.Response, error) {
		approved++
		if r.URL.Host != "10.42.1.7:8080" {
			t.Fatal("unexpected destination")
		}
		data := []byte("apiVersion: v1\nentries: {}\n")
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header), Request: r}, nil
	})}
	publicClient := &http.Client{Transport: catalogNetworkRoundTrip(func(r *http.Request) (*http.Response, error) { fallback++; return nil, io.EOF })}
	ctx := testRuntimeContext(deps)
	repo := sqlc.HelmRepository{ID: uuid.New(), Name: "fixture", Url: "http://10.42.1.7:8080", RepoType: "helm"}
	if _, err := fetchRepositoryIndex(ctx, publicClient, repo.Url+"/index.yaml", repo); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"https://1.1.1.1/index.yaml", "http://10.42.1.8:8080/index.yaml", "http://169.254.169.254/index.yaml"} {
		if _, err := fetchRepositoryIndex(ctx, publicClient, source, repo); err == nil {
			t.Fatal("unapproved fallback permitted")
		}
	}
	if approved != 1 || fallback != 0 {
		t.Fatalf("fetch counts: approved=%d fallback=%d", approved, fallback)
	}
}

func TestCatalogNetworkConfigurationRejectsInvalidPolicy(t *testing.T) {
	for _, raw := range []string{"null", `{"restricted":true,"origins":[{"url":"http://10.0.0.1","cidrs":["10.0.0.0/7"]}]}`} {
		if err := ConfigureCatalogNetwork(&RuntimeDependencies{}, &config.Config{DistributionDestinationPolicy: raw}); err == nil {
			t.Fatal("invalid policy admitted")
		}
	}
}
