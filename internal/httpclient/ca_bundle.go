package httpclient

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
)

const MaximumCertificateAuthorityBytes = 256 << 10
const MaximumCertificateAuthorities = 64

// ParseCertificateAuthorities creates a pool containing exactly the bounded
// supplied CA certificates. It never adds ambient system roots. Callers choose
// independently whether to combine these certificates with a system pool.
func ParseCertificateAuthorities(material []byte) (*x509.CertPool, error) {
	if len(material) == 0 || len(material) > MaximumCertificateAuthorityBytes {
		return nil, errors.New("certificate authority bundle outside bounds")
	}
	pool := x509.NewCertPool()
	count := 0
	for remaining := bytes.TrimSpace(material); len(remaining) != 0; {
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errors.New("certificate authority bundle invalid")
		}
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("certificate authority bundle invalid")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !certificate.IsCA {
			return nil, errors.New("certificate authority bundle invalid")
		}
		count++
		if count > MaximumCertificateAuthorities {
			return nil, errors.New("certificate authority bundle outside bounds")
		}
		pool.AddCert(certificate)
		remaining = bytes.TrimSpace(rest)
	}
	if count == 0 {
		return nil, errors.New("certificate authority bundle invalid")
	}
	return pool, nil
}
