package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestArgoCDUIProxy_ForwardsPathUnchanged verifies that the proxy forwards
// the full incoming path (including the `/argocd` prefix) to the upstream.
// That contract holds because argocd-server is configured with
// `server.rootpath: /argocd`, so the upstream itself routes on the prefix.
func TestArgoCDUIProxy_ForwardsPathUnchanged(t *testing.T) {
	// Fake upstream argocd-server; record the request it sees.
	var gotPath string
	var gotHost string
	var gotXFP string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHost = r.Host
		gotXFP = r.Header.Get("X-Forwarded-Proto")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<title>Argo CD</title>`))
	}))
	defer upstream.Close()

	proxy, err := NewArgoCDUIProxy(upstream.URL, nil)
	if err != nil {
		t.Fatalf("NewArgoCDUIProxy: %v", err)
	}

	req := httptest.NewRequest("GET", "/argocd/applications?foo=bar", nil)
	req.Host = "astronomer.example"
	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body, _ := io.ReadAll(rr.Body)
	if !strings.Contains(string(body), "Argo CD") {
		t.Errorf("body did not contain Argo CD title: %s", string(body))
	}
	if gotPath != "/argocd/applications" {
		t.Errorf("upstream path = %q, want /argocd/applications", gotPath)
	}
	if gotXFP != "http" {
		t.Errorf("X-Forwarded-Proto = %q, want http", gotXFP)
	}
	// Host header is rewritten to the upstream service; the original public
	// host is preserved in X-Forwarded-Host (when set by the wrapping
	// caller — we only set it from X-Original-Host). gotHost should be
	// the upstream's authority, NOT astronomer.example.
	if strings.Contains(gotHost, "astronomer.example") {
		t.Errorf("Host = %q leaked client hostname; expected upstream", gotHost)
	}
}

// TestArgoCDUIProxy_BadGatewayOnUpstreamFailure ensures we fail closed when
// the upstream is unreachable rather than panicking or hanging.
func TestArgoCDUIProxy_BadGatewayOnUpstreamFailure(t *testing.T) {
	// Use a port nothing is listening on. 127.0.0.1:1 is virtually
	// guaranteed to refuse — port 1 is privileged and not bound on
	// developer machines or CI runners.
	proxy, err := NewArgoCDUIProxy("http://127.0.0.1:1", nil)
	if err != nil {
		t.Fatalf("NewArgoCDUIProxy: %v", err)
	}
	req := httptest.NewRequest("GET", "/argocd/", nil)
	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rr.Code)
	}
}
