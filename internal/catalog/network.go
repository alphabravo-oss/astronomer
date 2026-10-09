package catalog

import (
	"context"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

type networkClientKey struct{}

// WithNetworkClient injects a composition-built bounded client into one catalog
// operation, including OCI token, tag, blob, and optional registry listing reads.
func WithNetworkClient(ctx context.Context, client *http.Client) context.Context {
	return context.WithValue(ctx, networkClientKey{}, client)
}
func catalogNetworkClient(ctx context.Context, timeout time.Duration, maximum int64) *http.Client {
	if client, ok := ctx.Value(networkClientKey{}).(*http.Client); ok && client != nil {
		return client
	}
	return httpclient.SafeClientWithLimit(timeout, maximum)
}
func catalogNetworkGuard(ctx context.Context, raw string) error {
	if client, ok := ctx.Value(networkClientKey{}).(*http.Client); ok && client != nil {
		return nil
	} // injected transport validates requests and every connect
	return httpclient.GuardPublicHost(raw)
}
