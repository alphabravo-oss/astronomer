package vault

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// SEC-R06: DefaultClientFactory must install a dial-guarded HTTP client that
// still blocks loopback/metadata while AllowPrivate is enabled for Vault.
func TestDefaultClientFactory_BlocksLoopbackDial(t *testing.T) {
	// Ensure guard is enabled (not DisableGuardForTest).
	client, err := DefaultClientFactory(sqlc.VaultConnection{
		Addr:       "http://127.0.0.1:8200",
		AuthMethod: "token",
	}, `{"token":"s.not-a-real-token"}`)
	if err != nil {
		t.Fatalf("DefaultClientFactory: %v", err)
	}
	vc, ok := client.(*vaultClient)
	if !ok {
		t.Fatalf("client type = %T", client)
	}
	httpClient := vc.api.CloneConfig().HttpClient
	if httpClient == nil {
		t.Fatal("vault HttpClient must be set")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	httpClient.Timeout = 2 * time.Second
	_, err = httpClient.Get(srv.URL)
	if err == nil {
		t.Fatal("vault client dialed loopback; want SafeClient block")
	}
}

// SEC-R06: SafeClientAllowPrivate still blocks metadata; factory uses that policy.
func TestSafeClientAllowPrivate_BlocksMetadata(t *testing.T) {
	// Hermetic: Control hook rejects link-local without a real dial.
	c := httpclient.SafeClientAllowPrivate(2 * time.Second)
	req, err := http.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data/", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(req)
	if err == nil {
		t.Fatal("AllowPrivate client must block link-local metadata")
	}
}
