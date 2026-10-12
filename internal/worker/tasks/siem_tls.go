package tasks

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// httpClientForForwarder returns a pooled *http.Client for the forwarder,
// building it once and reusing it across drains. The HEC/NDJSON transports
// previously allocated a fresh *http.Transport on every 2s tick (Close() is a
// no-op, so nothing was ever reused) — thousands of redundant TLS handshakes an
// hour under sustained load. Caching keyed by (tls_skip_verify, ca_cert_pem,
// timeout) keeps a single keep-alive pool per forwarder while still rebuilding
// when the operator changes TLS settings.
func (runtime DispatchRuntime) httpClientForSIEMForwarder(sub sqlc.SiemForwarder, timeout time.Duration) (*http.Client, error) {
	key := fmt.Sprintf("%t|%d|%s", sub.TlsSkipVerify, int64(timeout), sub.CaCertPem)
	runtime.siemState.httpClientMu.Lock()
	defer runtime.siemState.httpClientMu.Unlock()
	if c, ok := runtime.siemState.httpClients[sub.ID]; ok && c.key == key {
		return c.client, nil
	}
	client, err := buildHTTPClient(sub, timeout)
	if err != nil {
		return nil, err
	}
	runtime.siemState.httpClients[sub.ID] = cachedSIEMHTTPClient{key: key, client: client}
	return client, nil
}

// buildHTTPClient applies the per-forwarder TLS toggles to an *http.Client
// that still enforces dial-time public-IP checks (SEC-03 SafeClient).
// When tls_skip_verify is on, we log a warn at startup elsewhere; the
// client itself just honors the operator's choice.
func buildHTTPClient(sub sqlc.SiemForwarder, timeout time.Duration) (*http.Client, error) {
	cfg, err := buildTLSConfig(sub)
	if err != nil {
		return nil, err
	}
	return httpclient.SafeClientWithTLS(timeout, cfg), nil
}

// buildTLSConfig assembles the tls.Config from the forwarder's
// tls_skip_verify + ca_cert_pem columns. With no custom CA, nil RootCAs
// selects the system roots. Malformed custom anchors never select a fallback.
func buildTLSConfig(sub sqlc.SiemForwarder) (*tls.Config, error) {
	cfg := &tls.Config{InsecureSkipVerify: sub.TlsSkipVerify} //nolint:gosec // operator-controlled toggle
	if strings.TrimSpace(sub.CaCertPem) != "" {
		pool := x509.NewCertPool()
		if ok := pool.AppendCertsFromPEM([]byte(sub.CaCertPem)); !ok {
			return nil, errors.New("ca_cert_pem could not be parsed")
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}
