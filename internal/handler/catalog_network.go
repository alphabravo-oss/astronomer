package handler

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func (h *CatalogHandler) SetCatalogNetwork(policy httpclient.DestinationPolicy, trust *tls.Config, proxy string) error {
	policy = policy.Clone()
	client, err := httpclient.NewDestinationClient(policy, httpclient.DestinationOptions{AllowPublicCrossOriginRedirects: !policy.Restricted, SensitiveHeaders: []string{"Private-Token", "X-Gitlab-Token", "X-Auth-Token", "X-Api-Key"}, TLSConfig: trust, Timeout: 30 * time.Second, MaxResponseBytes: catalog.MaxOCIResponseBytes, ProxyURL: proxy})
	if err != nil {
		return err
	}
	h.catalogPolicy = &policy
	h.catalogHTTPClient = client
	return nil
}
func (h *CatalogHandler) guardCatalogURL(ctx context.Context, raw string) error {
	if h.catalogPolicy != nil {
		return h.catalogPolicy.ValidateURL(ctx, raw)
	}
	return httpclient.GuardPublicHost(raw)
}
func (h *CatalogHandler) catalogClient(timeout time.Duration, maximum int64) *http.Client {
	if h.catalogHTTPClient != nil {
		return h.catalogHTTPClient
	}
	return httpclient.SafeClientWithLimit(timeout, maximum)
}
func (h *CatalogHandler) catalogContext(ctx context.Context) context.Context {
	if h.catalogHTTPClient != nil {
		return catalog.WithNetworkClient(ctx, h.catalogHTTPClient)
	}
	return ctx
}
