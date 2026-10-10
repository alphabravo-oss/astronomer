package main

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// Only operator registry tools compose this client. The independently supplied
// operator policy must explicitly approve both literal origins and DNS ranges.
// Runtime configuration, catalog URLs and signed asset content cannot select
// redirect authority. All checks remain in the canonical destination client.
func registryToolDestinationOptions(network httpclient.DestinationPolicy, trust *tls.Config) (httpclient.DestinationOptions, error) {
	options := httpclient.DestinationOptions{TLSConfig: trust, MaxResponseBytes: distribution.MaximumArchiveBytes}
	if err := network.Validate(); err != nil {
		return options, err
	}
	registry, cdn := false, false
	for _, approved := range network.Origins {
		origin, err := url.Parse(approved.URL)
		if err != nil || origin.Scheme != "https" || origin.Port() != "" && origin.Port() != "443" {
			continue
		}
		switch strings.ToLower(origin.Hostname()) {
		case "ghcr.io":
			registry = true
		case "pkg-containers.githubusercontent.com":
			cdn = true
		}
	}
	if registry && cdn {
		options.RedirectOriginPairs = []httpclient.RedirectOriginPair{{From: "https://ghcr.io", To: "https://pkg-containers.githubusercontent.com"}}
	}
	return options, nil
}

func newRegistryToolDestinationClient(network httpclient.DestinationPolicy, trust *tls.Config) (*http.Client, error) {
	options, err := registryToolDestinationOptions(network, trust)
	if err != nil {
		return nil, err
	}
	return httpclient.NewDestinationClient(network, options)
}
