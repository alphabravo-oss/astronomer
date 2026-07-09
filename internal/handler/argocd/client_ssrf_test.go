package argocd

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// SEC-R05: default NewClient (no injected HTTPClient) must refuse loopback dials.
func TestNewClient_DefaultSafeClientBlocksLoopback(t *testing.T) {
	c := NewClient("https://argocd.example.invalid", "tok", Options{VerifySSL: true, Timeout: 2 * time.Second})
	if c.httpClient == nil {
		t.Fatal("httpClient must be set")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := c.httpClient
	client.Timeout = 2 * time.Second
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("argocd client dialed loopback; want SafeClient block")
	}
}
