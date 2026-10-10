package helmruntime

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// RepositoryClient uses operator-owned policy, independently of chart URLs,
// catalog credentials or delivery/distribution authority. Request and body
// ownership is bound to the operation; canonical destination checks still
// enforce actual dial addresses and every redirect.
func RepositoryClient(ctx context.Context, runtime Config, trust *tls.Config) (*http.Client, error) {
	if ctx == nil {
		return nil, errors.New("Helm repository context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := httpclient.ValidateProxyOrigin(runtime.RepositoryProxyURL); err != nil {
		return nil, err
	}
	policy, err := httpclient.ParseDestinationPolicy(runtime.RepositoryDestinationPolicy)
	if err != nil {
		return nil, err
	}
	client, err := httpclient.NewDestinationClientContext(ctx, policy, httpclient.DestinationOptions{
		Timeout: 2 * time.Minute, TLSConfig: trust, MaxResponseBytes: 64 << 20,
		MaxRedirects: 5, AllowPublicCrossOriginRedirects: !policy.Restricted,
		ProxyURL: runtime.RepositoryProxyURL,
	})
	if err != nil {
		return nil, err
	}
	return HTTPClient(ctx, client)
}
