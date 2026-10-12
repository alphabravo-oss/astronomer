package resolver

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// PublicationSupplierClient belongs only to the protected connected producer.
// Its one literal supplier/CDN transition requires BOTH independently approved
// constructor origins. It reuses this owner's exact frozen roots and policy;
// feeds, registry responses and configuration flags cannot choose a new pair.
// Ordinary loading/refresh clients remain strict same-origin. The synchronous
// caller must close idle connections after joining transfers; this does not
// promise that Close drains an active body or an escaped caller goroutine.
func (transport *DatabaseRegistryTransport) PublicationSupplierClient(ctx context.Context) (*http.Client, error) {
	if ctx == nil || transport == nil {
		return nil, errors.New("database supplier transport owner unavailable")
	}
	if err := transport.Revalidate(ctx); err != nil {
		return nil, err
	}
	transport.mutex.RLock()
	defer transport.mutex.RUnlock()
	if transport.closed {
		return nil, errors.New("database supplier transport owner closed")
	}
	pool, err := httpclient.ParseCertificateAuthorities(transport.rootMaterial)
	if err != nil {
		return nil, errors.New("database supplier frozen roots invalid")
	}
	client, err := httpclient.NewDestinationClientContext(ctx, transport.network, httpclient.DestinationOptions{
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, Timeout: 30 * time.Second, MaxResponseBytes: 128 << 20, MaxRedirects: 2,
		RedirectOriginPairs: []httpclient.RedirectOriginPair{{From: "https://ghcr.io", To: "https://pkg-containers.githubusercontent.com"}},
	})
	if err != nil {
		return nil, errors.New("database supplier requires both explicit finite origins")
	}
	base := client.Transport
	client.Transport = databaseSupplierRoundTripper{owner: transport, base: base}
	return client, nil
}

type databaseSupplierRoundTripper struct {
	owner *DatabaseRegistryTransport
	base  http.RoundTripper
}

func (round databaseSupplierRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || round.owner == nil || round.base == nil {
		return nil, errors.New("database supplier owned request unavailable")
	}
	if err := round.owner.Revalidate(request.Context()); err != nil {
		return nil, err
	}
	return round.base.RoundTrip(request)
}
func (round databaseSupplierRoundTripper) CloseIdleConnections() {
	if closer, ok := round.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
