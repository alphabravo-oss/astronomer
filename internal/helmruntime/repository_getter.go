package helmruntime

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"helm.sh/helm/v4/pkg/getter"
	repo "helm.sh/helm/v4/pkg/repo/v1"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

const (
	maximumRepositoryIndex = 8 << 20
	maximumRepositoryChart = 64 << 20
)

type repositoryGetter struct {
	ctx     context.Context
	runtime Config
	owner   *repo.Entry
}

// Get satisfies the SDK Getter interface. Its opaque options cannot widen
// captured operator credentials/TLS/destination authority. Our caller exposes
// only chart coordinates and version; Accept covers both charts and indexes.
func (g *repositoryGetter) Get(address string, _ ...getter.Option) (*bytes.Buffer, error) {
	if g == nil || g.ctx == nil {
		return nil, errors.New("Helm repository context is required")
	}
	if err := g.ctx.Err(); err != nil {
		return nil, err
	}
	client, err := g.client(address)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	request, err := http.NewRequestWithContext(g.ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("Helm repository request is invalid")
	}
	request.Header.Set("Accept", "application/gzip,application/octet-stream,application/x-yaml,text/yaml;q=0.9")
	request.Header.Set("User-Agent", "astronomer-helm-repository/1.0")
	if g.credentialsFor(address) && g.owner.Username != "" && g.owner.Password != "" {
		request.SetBasicAuth(g.owner.Username, g.owner.Password)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, chartLookupError(g.ctx, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("Helm repository returned an unsuccessful status")
	}
	maximum := int64(maximumRepositoryChart)
	if request.URL != nil && strings.HasSuffix(request.URL.Path, "/index.yaml") {
		maximum = maximumRepositoryIndex
	}
	value, err := httpclient.ReadBoundedResponse(g.ctx, response, maximum)
	if err != nil {
		return nil, err
	}
	return bytes.NewBuffer(value), nil
}

func (g *repositoryGetter) credentialsFor(address string) bool {
	return g.owner != nil && (sameRepositoryOrigin(g.owner.URL, address) || g.owner.PassCredentialsAll)
}

func (g *repositoryGetter) client(address string) (*http.Client, error) {
	caFile := ""
	if g.owner != nil {
		caFile = g.owner.CAFile
	}
	trust, err := httpclient.TLSForCA(caFile)
	if err != nil {
		return nil, err
	}
	if g.owner != nil && g.credentialsFor(address) && (g.owner.CertFile != "" || g.owner.KeyFile != "") {
		certificate, err := tls.LoadX509KeyPair(g.owner.CertFile, g.owner.KeyFile)
		if err != nil {
			return nil, errors.New("Helm repository client certificate is unavailable")
		}
		trust.Certificates = []tls.Certificate{certificate}
	}
	client, err := RepositoryClient(g.ctx, g.runtime, trust)
	if err != nil {
		return nil, err
	}
	if len(trust.Certificates) != 0 {
		check := client.CheckRedirect
		client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
			if err := check(request, via); err != nil {
				return err
			}
			// Canonical redirects strip bearer/basic headers. A TLS client
			// certificate is transport-scoped, so prevent cross-origin use.
			if len(via) != 0 && !sameRepositoryOrigin(via[0].URL.String(), request.URL.String()) {
				return errors.New("Helm client-certificate redirect changes origin")
			}
			return nil
		}
	}
	return client, nil
}

// HTTPRepositoryReference distinguishes supported HTTP transport from local
// chart paths. It grants no destination permission.
func HTTPRepositoryReference(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
}
