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
func TestSafeTransport_UsesDialGuard(t *testing.T) {
	tr := SafeTransport(nil)
	if tr == nil || tr.DialContext == nil {
		t.Fatal("SafeTransport must set DialContext with rebinding guard")
	}
}

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

// SEC-R04/R05/R06: AllowPrivate still blocks loopback and metadata.
func TestSafeClientAllowPrivate_BlocksLoopbackAndMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	_, err := SafeClientAllowPrivate(2 * time.Second).Get(srv.URL)
	if err == nil {
		t.Fatal("AllowPrivate must still block loopback dial")
	}
	_, err = SafeClientAllowPrivate(2 * time.Second).Get("http://169.254.169.254/latest/meta-data/")
	if err == nil {
		t.Fatal("AllowPrivate must still block link-local metadata")
	}
}
