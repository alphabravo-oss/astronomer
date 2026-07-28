package argocd

import (
	"net/http"
	"testing"
)

// Options is stated in the skip direction so the zero value verifies: it used
// to be a VerifySSL bool, where a caller that omitted the field got an
// unverified connection to the ArgoCD API it sends an admin bearer token to.
func TestNewClient_ZeroOptionsVerifiesTLS(t *testing.T) {
	c := NewClient("https://argocd.example.invalid", "tok", Options{})
	if transportSkipsVerify(t, c.httpClient) {
		t.Fatal("zero Options produced an InsecureSkipVerify transport")
	}
}

func TestNewClient_SkipTLSVerifyOptsOut(t *testing.T) {
	c := NewClient("https://argocd.example.invalid", "tok", Options{SkipTLSVerify: true})
	if !transportSkipsVerify(t, c.httpClient) {
		t.Fatal("SkipTLSVerify=true did not disable verification")
	}
}

func TestHTTPClientFor_OnlySkipsWhenAsked(t *testing.T) {
	if transportSkipsVerify(t, HTTPClientFor(0, false)) {
		t.Fatal("HTTPClientFor(_, false) skipped verification")
	}
	if !transportSkipsVerify(t, HTTPClientFor(0, true)) {
		t.Fatal("HTTPClientFor(_, true) verified")
	}
}

func transportSkipsVerify(t *testing.T, c *http.Client) bool {
	t.Helper()
	transport, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", c.Transport)
	}
	return transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify
}
