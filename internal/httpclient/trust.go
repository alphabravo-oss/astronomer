package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"os"
)

// TLSForCA loads bounded, operator-mounted trust without a network key fetch.
func TLSForCA(filename string) (*tls.Config, error) {
	result := &tls.Config{MinVersion: tls.VersionTLS12}
	if filename == "" {
		return result, nil
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, errors.New("destination CA file is unavailable")
	}
	// Read-only descriptor cleanup does not replace the trust validation result.
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("destination CA file is invalid")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) == 0 || len(data) > 1<<20 {
		return nil, errors.New("destination CA file exceeds bounds")
	}
	defer clear(data)
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(data) {
		return nil, errors.New("destination CA file contains no certificates")
	}
	result.RootCAs = roots
	return result, nil
}
