package agent

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strings"
)

// BuildTLSConfig creates a *tls.Config for server-CA pinning on the agent
// tunnel, implementing Rancher CATTLE_CA_CHECKSUM semantics.
//
//   - If neither caCert nor caChecksum is provided, it returns (nil, nil) so the
//     dialer falls back to the OS trust store with standard verification. This is
//     the DEFAULT path and is byte-for-byte behavior-equivalent to passing no
//     TLS config at all — no InsecureSkipVerify, no RootCAs override.
//   - If caCert (PEM) is provided, it is loaded into a fresh x509.CertPool and
//     set as RootCAs so a private/self-signed management CA is trusted.
//   - If caChecksum (hex sha256) is provided, a VerifyConnection callback is
//     installed that computes the SHA-256 over the server-presented CA/cert
//     chain and requires a match to the pinned checksum. It FAILS CLOSED
//     (returns an error, refusing the connection) on any mismatch.
//
// caChecksum without caCert is rejected: pinning a checksum is meaningless
// unless the corresponding CA bundle is also trusted, and silently accepting it
// would leave standard OS-trust verification as the only gate (a footgun).
func BuildTLSConfig(caCert, caChecksum string) (*tls.Config, error) {
	caCert = strings.TrimSpace(caCert)
	caChecksum = strings.TrimSpace(caChecksum)

	if caCert == "" && caChecksum == "" {
		// Default: OS trust store, standard verification, no behavior change.
		return nil, nil
	}

	if caCert == "" && caChecksum != "" {
		return nil, fmt.Errorf("ca_checksum set without ca_cert: cannot pin a checksum without the trusted CA bundle")
	}

	// Enforce a modern floor consistent with the rest of the codebase
	// (vault/remoteproxy/email all pin tls.VersionTLS12).
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caCert)) {
		return nil, fmt.Errorf("ca_cert: could not parse any certificate from PEM")
	}
	cfg.RootCAs = pool

	if caChecksum != "" {
		expected := strings.ToLower(caChecksum)
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return fmt.Errorf("ca pin: no peer certificates presented (FAIL CLOSED)")
			}
			// Rancher CATTLE_CA_CHECKSUM semantics: the pin is the SHA-256 of the
			// configured CA cert. Match it against ANY certificate in the
			// server-presented chain (leaf, intermediate, or root) so the pin is
			// robust to chain ordering and to whichever cert in the configured
			// bundle the operator pinned — the renderer hashes the first cert in
			// registration.ca_bundle, and the server must present that exact cert
			// somewhere in its chain. Standard RootCAs verification already ran
			// (no InsecureSkipVerify), so this is an additional pin, not the only
			// gate. Fails closed if no chain cert matches.
			for _, cert := range cs.PeerCertificates {
				if strings.ToLower(fmt.Sprintf("%x", sha256.Sum256(cert.Raw))) == expected {
					return nil
				}
			}
			return fmt.Errorf("ca pin: no certificate in the server-presented chain matches the pinned CA SHA-256 %s (FAIL CLOSED)", expected)
		}
	}

	return cfg, nil
}
