package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

func TestTrustedRealIPRejectsSpoofedHeadersFromUntrustedPeer(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.20:4321"
	request.Header.Set("True-Client-IP", "10.0.0.8")
	request.Header.Set("X-Forwarded-For", "10.0.0.9")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "evil.example")
	TrustedRealIP("10.42.0.0/16")(http.HandlerFunc(func(_ http.ResponseWriter, got *http.Request) {
		if got.RemoteAddr != request.RemoteAddr {
			t.Fatalf("untrusted peer rewrote RemoteAddr to %q", got.RemoteAddr)
		}
		if got.Header.Get("True-Client-IP") != "" {
			t.Fatal("spoofable True-Client-IP survived")
		}
		for _, header := range forwardingHeaders {
			if value := got.Header.Get(header); value != "" {
				t.Fatalf("untrusted %s survived as %q", header, value)
			}
		}
		if reqctx.RequestIsHTTPS(got) {
			t.Fatal("untrusted X-Forwarded-Proto changed request scheme")
		}
		if host := reqctx.RequestHost(got); host != request.Host {
			t.Fatalf("RequestHost = %q, want %q", host, request.Host)
		}
	})).ServeHTTP(httptest.NewRecorder(), request)
}

func TestTrustedRealIPWalksTrustedChainRightToLeft(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.42.0.8:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.7, 10.43.0.9")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "astronomer.example")
	TrustedRealIP("10.42.0.0/16,10.43.0.0/16")(http.HandlerFunc(func(_ http.ResponseWriter, got *http.Request) {
		if got.RemoteAddr != "203.0.113.7" {
			t.Fatalf("RemoteAddr = %q", got.RemoteAddr)
		}
		if address := reqctx.ClientIP(got); address == nil || address.String() != "203.0.113.7" {
			t.Fatalf("ClientIP = %v", address)
		}
		if !reqctx.RequestIsHTTPS(got) {
			t.Fatal("trusted X-Forwarded-Proto was not honored")
		}
		if host := reqctx.RequestHost(got); host != "astronomer.example" {
			t.Fatalf("RequestHost = %q", host)
		}
		for _, header := range forwardingHeaders {
			if value := got.Header.Get(header); value != "" {
				t.Fatalf("consumed %s survived as %q", header, value)
			}
		}
	})).ServeHTTP(httptest.NewRecorder(), request)
}

func TestTrustedRealIPDoesNotAcceptSpoofedLeftmostHop(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.42.0.8:4321"
	request.Header.Set("X-Forwarded-For", "192.0.2.99, 203.0.113.7")
	TrustedRealIP("10.42.0.0/16")(http.HandlerFunc(func(_ http.ResponseWriter, got *http.Request) {
		if address := reqctx.ClientIP(got); address == nil || address.String() != "203.0.113.7" {
			t.Fatalf("ClientIP = %v, want nearest untrusted hop 203.0.113.7", address)
		}
	})).ServeHTTP(httptest.NewRecorder(), request)
}
