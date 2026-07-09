package monitoring

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// SEC-R04: NewClient must ship a dial-guarded client that blocks loopback
// even with AllowPrivate (loopback is never permitted).
func TestNewClient_UsesSafeClientBlocksLoopback(t *testing.T) {
	c, err := NewClient(BackendConfig{QueryURL: "http://127.0.0.1:9090", TimeoutSeconds: 2})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.httpClient == nil {
		t.Fatal("httpClient must be set")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := c.httpClient
	client.Timeout = 2 * time.Second
	_, err = client.Get(srv.URL)
	if err == nil {
		t.Fatal("prometheus client dialed loopback; want SafeClient block")
	}
}
