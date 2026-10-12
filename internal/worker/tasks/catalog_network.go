package tasks

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// ConfigureCatalogNetwork is called by both worker compositions. The policy is
// fixed operator configuration; database repository URLs never authorize it.
func ConfigureCatalogNetwork(deps *RuntimeDependencies, cfg *config.Config) error {
	policy, err := httpclient.ParseDestinationPolicy(cfg.DistributionDestinationPolicy)
	if err != nil {
		return err
	}
	trust, err := httpclient.TLSForCA(cfg.CatalogCAFile)
	if err != nil {
		return err
	}
	client, err := httpclient.NewDestinationClient(policy, httpclient.DestinationOptions{AllowPublicCrossOriginRedirects: !policy.Restricted, SensitiveHeaders: []string{"Private-Token", "X-Gitlab-Token", "X-Auth-Token", "X-Api-Key"}, TLSConfig: trust, Timeout: 30 * time.Second, MaxResponseBytes: catalog.MaxOCIResponseBytes, ProxyURL: cfg.CatalogProxyURL})
	if err != nil {
		return err
	}
	deps.CatalogNetwork = &policy
	deps.CatalogHTTPClient = client
	return nil
}
func catalogOperationContext(ctx context.Context) context.Context {
	if client := runtimeDependencies(ctx).CatalogHTTPClient; client != nil {
		return catalog.WithNetworkClient(ctx, client)
	}
	return ctx
}
func catalogFetchClient(ctx context.Context, fallback *http.Client) *http.Client {
	if client := runtimeDependencies(ctx).CatalogHTTPClient; client != nil {
		return client
	}
	return fallback
}
func guardCatalogFetch(ctx context.Context, raw string) error {
	if policy := runtimeDependencies(ctx).CatalogNetwork; policy != nil {
		return policy.ValidateURL(ctx, strings.Replace(raw, "oci://", "https://", 1))
	}
	return httpclient.GuardPublicHost(raw)
}

type catalogFetchStatusError struct{ status int }

func (e *catalogFetchStatusError) Error() string {
	return fmt.Sprintf("catalog repository returned status %d", e.status)
}
