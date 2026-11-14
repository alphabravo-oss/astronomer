package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// IdentitySettings describes the single trusted SSO issuer for this installation.
// Local accounts and authorization remain owned by Astronomer.
type IdentitySettings struct {
	Mode                                                                             string
	Issuer, ClientID, ClientSecret, JWKSURL, AuthorizationURL, TokenURL, RedirectURI string
	RolesClaim                                                                       string
}

func (c *Config) SharedIdentity() (IdentitySettings, error) {
	s := IdentitySettings{Mode: c.IdentityMode, Issuer: c.IdentityIssuer, ClientID: c.IdentityClientID, ClientSecret: c.IdentityClientSecret, JWKSURL: c.IdentityJWKSURL, AuthorizationURL: c.IdentityAuthorizationURL, TokenURL: c.IdentityTokenURL, RedirectURI: c.IdentityRedirectURI, RolesClaim: c.IdentityRolesClaim}
	if s.Mode == "" {
		s.Mode = "local"
	}
	if s.Mode != "local" && s.Mode != "both" {
		return s, fmt.Errorf("identity_mode must be local or both; local login cannot be disabled")
	}
	if s.Mode == "local" {
		return s, nil
	}
	if strings.TrimSpace(s.ClientID) == "" || strings.TrimSpace(s.ClientSecret) == "" {
		return s, fmt.Errorf("shared Identity requires client credentials")
	}
	for _, endpoint := range []struct{ name, value string }{{"issuer", s.Issuer}, {"JWKS", s.JWKSURL}, {"authorization", s.AuthorizationURL}, {"token", s.TokenURL}, {"redirect", s.RedirectURI}} {
		u, err := url.Parse(endpoint.value)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return s, fmt.Errorf("shared Identity %s must be an absolute HTTPS URL without credentials, query or fragment", endpoint.name)
		}
	}
	if s.RolesClaim == "" {
		s.RolesClaim = "alphabravo_roles"
	}
	return s, nil
}

// IdentityHTTPClient trusts the system roots plus an optional operator CA.
// Local-only mode does not read the file or require any Identity credentials.
func (c *Config) IdentityHTTPClient() (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if c.IdentityMode == "both" && c.IdentityCAFile != "" {
		pem, err := os.ReadFile(c.IdentityCAFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read Identity CA bundle")
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("Identity CA bundle contains no certificates")
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}, nil
}
