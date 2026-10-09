package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func TestInteractiveCatalogHydrationDoesNotFallbackOutsideApprovedOrigins(t *testing.T) {
	policy, err := httpclient.ParseDestinationPolicy(`{"restricted":true,"origins":[{"url":"https://10.42.1.7","cidrs":["10.42.1.7/32"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	h := &CatalogHandler{}
	if err := h.SetCatalogNetwork(policy, nil, ""); err != nil {
		t.Fatal(err)
	}
	requests := 0
	h.catalogHTTPClient = &http.Client{Transport: catalogHydrationNetworkTransport(func(r *http.Request) (*http.Response, error) { requests++; return nil, nil })}
	for _, target := range []string{"https://1.1.1.1/chart.tgz", "https://10.42.1.8/chart.tgz", "https://169.254.169.254/chart.tgz"} {
		urls, _ := json.Marshal([]string{target})
		if _, err := h.fetchHTTPChartArchive(t.Context(), sqlc.HelmRepository{Url: "https://10.42.1.7"}, sqlc.HelmChartVersion{Urls: urls}); err == nil {
			t.Fatal("unapproved chart hydration admitted")
		}
	}
	if requests != 0 {
		t.Fatal("interactive hydration fetched outside operator policy")
	}
}

func TestInteractiveOCILazyHydrationUsesApprovedInjectedTransport(t *testing.T) {
	policy, err := httpclient.ParseDestinationPolicy(`{"restricted":true,"origins":[{"url":"https://10.42.1.7","cidrs":["10.42.1.7/32"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	h := &CatalogHandler{}
	if err := h.SetCatalogNetwork(policy, nil, ""); err != nil {
		t.Fatal(err)
	}
	requests := 0
	h.catalogHTTPClient = &http.Client{Transport: catalogHydrationNetworkTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Host != "10.42.1.7" {
			t.Fatal("unexpected OCI destination")
		}
		// A registry-level failure proves the actual Helm OCI pull reached this
		// injected transport rather than failing the default public-host guard.
		return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil)), Request: r}, nil
	})}
	chart, version := sqlc.HelmChart{Name: "qa-chart"}, sqlc.HelmChartVersion{Version: "1.0.0"}
	if _, err := h.fetchOCIChartArchive(t.Context(), sqlc.HelmRepository{Url: "oci://10.42.1.7/charts"}, chart, version); err == nil || err.Error() != "OCI chart pull failed" {
		t.Fatalf("approved OCI path did not reach injected pull: %v", err)
	}
	if requests == 0 {
		t.Fatal("approved OCI lazy hydration skipped injected transport")
	}
	previous := requests
	for _, url := range []string{"oci://1.1.1.1/charts", "oci://10.42.1.8/charts", "oci://169.254.169.254/charts"} {
		if _, err := h.fetchOCIChartArchive(t.Context(), sqlc.HelmRepository{Url: url}, chart, version); err == nil || err.Error() != "OCI chart source blocked" {
			t.Fatalf("unapproved OCI source was not blocked before pull: %v", err)
		}
	}
	if requests != previous {
		t.Fatal("disallowed OCI source reached injected or public transport")
	}
}

type catalogHydrationNetworkTransport func(*http.Request) (*http.Response, error)

func (f catalogHydrationNetworkTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
