package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTrustedRealIPRejectsSpoofedHeadersFromUntrustedPeer(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.20:4321"
	request.Header.Set("True-Client-IP", "10.0.0.8")
	request.Header.Set("X-Forwarded-For", "10.0.0.9")
	TrustedRealIP("10.42.0.0/16")(http.HandlerFunc(func(_ http.ResponseWriter, got *http.Request) {
		if got.RemoteAddr != request.RemoteAddr {
			t.Fatalf("untrusted peer rewrote RemoteAddr to %q", got.RemoteAddr)
		}
		if got.Header.Get("True-Client-IP") != "" {
			t.Fatal("spoofable True-Client-IP survived")
		}
	})).ServeHTTP(httptest.NewRecorder(), request)
}

func TestTrustedRealIPWalksTrustedChainRightToLeft(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.42.0.8:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.7, 10.43.0.9")
	TrustedRealIP("10.42.0.0/16,10.43.0.0/16")(http.HandlerFunc(func(_ http.ResponseWriter, got *http.Request) {
		if got.RemoteAddr != "203.0.113.7" {
			t.Fatalf("RemoteAddr = %q", got.RemoteAddr)
		}
	})).ServeHTTP(httptest.NewRecorder(), request)
}
