package handler

import (
	"encoding/json"
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

type catalogHydrationNetworkTransport func(*http.Request) (*http.Response, error)

func (f catalogHydrationNetworkTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
