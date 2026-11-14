package config

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIdentityPrivateCATrustAndLocalIndependence(t *testing.T) {
	t.Parallel()
	issuer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(issuer.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuer.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{IdentityMode: "both", IdentityCAFile: ca}
	client, err := c.IdentityHTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if client.Timeout != 10*time.Second {
		t.Fatal("identity requests must be bounded")
	}
	response, err := client.Get(issuer.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal("trusted identity endpoint failed")
	}
	c.IdentityCAFile = filepath.Join(t.TempDir(), "absent.pem")
	if _, err = c.IdentityHTTPClient(); err == nil {
		t.Fatal("missing private CA accepted for optional SSO")
	}
	c.IdentityMode = "local"
	local, err := c.IdentityHTTPClient()
	if err != nil {
		t.Fatalf("local authentication read an optional identity CA: %v", err)
	}
	defer local.CloseIdleConnections()
	if response, err = local.Get(issuer.URL); err == nil {
		_ = response.Body.Close()
		t.Fatal("local default client unexpectedly trusts private provider certificate")
	}
}
