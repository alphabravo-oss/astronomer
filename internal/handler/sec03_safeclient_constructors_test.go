package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// SEC-03: NewDefaultCloudTester must ship SafeClient so the wired server path
// (server.go) cannot dial loopback/metadata after a GuardPublicHost pass.
func TestNewDefaultCloudTester_UsesSafeClient(t *testing.T) {
	tester := NewDefaultCloudTester()
	if tester.HTTPClient == nil {
		t.Fatal("HTTPClient must be set by constructor (SafeClient)")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Guard is enabled by default; SafeClient must refuse loopback dial.
	client := tester.HTTPClient
	client.Timeout = 2 * time.Second
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("shipped NewDefaultCloudTester client dialed loopback; want SafeClient block")
	}
}

// SEC-03: NewBackupHandler must default httpClient to SafeClient so S3 probe
// path never uses a plain client when the handler is constructed normally.
func TestNewBackupHandler_UsesSafeClient(t *testing.T) {
	h := NewBackupHandler(nil)
	if h.httpClient == nil {
		t.Fatal("httpClient must be set")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := h.httpClient
	client.Timeout = 2 * time.Second
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("shipped NewBackupHandler client dialed loopback; want SafeClient block")
	}
}
