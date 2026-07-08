package httpclient

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// SafeClient must refuse to dial a loopback/private address even when the URL
// passed GuardPublicHost (or when the guard is bypassed at the URL layer),
// which is the DNS-rebinding defense: the dialer re-checks the connected IP.
func TestSafeClient_BlocksLoopbackDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// srv.URL is http://127.0.0.1:PORT — a loopback the dial guard must reject.
	_, err := SafeClient(2 * time.Second).Get(srv.URL)
	if err == nil {
		t.Fatal("SafeClient dialed a loopback address; expected the dial guard to block it")
	}
}

func TestSafeClient_AllowsWhenDisabledForTest(t *testing.T) {
	defer DisableGuardForTest()()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	resp, err := SafeClient(2 * time.Second).Get(srv.URL)
	if err != nil {
		t.Fatalf("with guard disabled, loopback dial should succeed: %v", err)
	}
	_ = resp.Body.Close()
}
