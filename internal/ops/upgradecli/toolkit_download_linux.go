//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func newToolkitDownloadClient(ctx context.Context) (*http.Client, error) {
	client, err := httpclient.NewDestinationClientContext(ctx, httpclient.DestinationPolicy{}, httpclient.DestinationOptions{
		MaxResponseBytes: toolkitMaximum, MaxRedirects: 3, AllowPublicCrossOriginRedirects: true,
	})
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = toolkitDownloadRedirects(client.CheckRedirect)
	return client, nil
}

// The authority fixes every download URL and digest. GitHub serves its bytes
// through one separate HTTPS origin; allow only that transition, then retain
// the shared client's public-DNS, credential-stripping and redirect bounds.
func toolkitDownloadRedirects(guard func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, via []*http.Request) error {
		if request == nil || request.URL == nil || len(via) == 0 || via[len(via)-1] == nil || via[len(via)-1].URL == nil {
			return errors.New("toolkit redirect chain is invalid")
		}
		from, to := via[len(via)-1].URL, request.URL
		sameOrigin := from.Scheme == "https" && to.Scheme == "https" && strings.EqualFold(from.Host, to.Host)
		githubAsset := toolkitHTTPSOrigin(from, "github.com") && toolkitHTTPSOrigin(to, "release-assets.githubusercontent.com")
		if !sameOrigin && !githubAsset {
			return errors.New("toolkit redirect origin is not approved")
		}
		return guard(request, via)
	}
}

func toolkitHTTPSOrigin(value *url.URL, hostname string) bool {
	return value.Scheme == "https" && value.User == nil && strings.EqualFold(value.Hostname(), hostname) && (value.Port() == "" || value.Port() == "443")
}
