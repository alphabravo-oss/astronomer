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

func TestArgoCDUIProxySanitizesResponseHeadersAndCookies(t *testing.T) {
	var gotAuth string
	var gotCookie string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Authorization", "Bearer leaked")
		w.Header().Set("Clear-Site-Data", `"cookies"`)
		w.Header().Set("Connection", "upgrade")
		w.Header().Set("Content-Length", "11")
		w.Header().Set("Proxy-Authenticate", "Basic")
		w.Header().Set("Set-Cookie", "astronomer_session=leaked; Path=/; HttpOnly")
		w.Header().Add("Set-Cookie", "argocd.token=upstream; Path=/; Domain=example.com")
		w.Header().Set("Set-Cookie2", "legacy=leaked")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	proxy, err := NewArgoCDUIProxy(upstream.URL, nil)
	if err != nil {
		t.Fatalf("NewArgoCDUIProxy: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/argocd/api/v1/session/userinfo", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer astronomer")
	req.Header.Set("Cookie", "astronomer_session=secret; theme=dark")
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if gotAuth != "" {
		t.Fatalf("upstream Authorization leaked: %q", gotAuth)
	}
	if strings.Contains(gotCookie, "astronomer_session=") {
		t.Fatalf("upstream Cookie leaked astronomer_session: %q", gotCookie)
	}
	if !strings.Contains(gotCookie, "theme=dark") {
		t.Fatalf("expected unrelated cookie to be preserved upstream, got %q", gotCookie)
	}
	for _, header := range []string{
		"Authorization",
		"Clear-Site-Data",
		"Connection",
		"Content-Length",
		"Proxy-Authenticate",
		"Set-Cookie2",
		"Transfer-Encoding",
		"WWW-Authenticate",
	} {
		if got := rr.Header().Get(header); got != "" {
			t.Fatalf("expected unsafe response header %s to be stripped, got %q", header, got)
		}
	}
	if rr.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("expected Cache-Control to be preserved")
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v, want only argocd.token", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != "argocd.token" || cookie.Value != "upstream" {
		t.Fatalf("cookie = %s=%s, want argocd.token=upstream", cookie.Name, cookie.Value)
	}
	if cookie.Domain != "" {
		t.Fatalf("cookie domain = %q, want host-only", cookie.Domain)
	}
	if cookie.Path != "/argocd" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie security attrs = path:%q httponly:%v secure:%v samesite:%v", cookie.Path, cookie.HttpOnly, cookie.Secure, cookie.SameSite)
	}
}

func TestArgoCDUIProxyAuditsDocumentAndMutatingRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/assets/") {
			w.Header().Set("Content-Type", "application/javascript")
		} else {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer upstream.Close()

	proxy, err := NewArgoCDUIProxy(upstream.URL, nil)
	if err != nil {
		t.Fatalf("NewArgoCDUIProxy: %v", err)
	}
	audit := &serviceProxyTestAuditWriter{}
	proxy.SetAuditWriter(audit)

	docReq := httptest.NewRequest(http.MethodGet, "/argocd/applications", nil)
	docReq.Header.Set("Accept", "text/html")
	proxy.ServeHTTP(httptest.NewRecorder(), docReq)

	assetReq := httptest.NewRequest(http.MethodGet, "/argocd/assets/main.js", nil)
	assetReq.Header.Set("Accept", "*/*")
	proxy.ServeHTTP(httptest.NewRecorder(), assetReq)

	mutatingReq := httptest.NewRequest(http.MethodPost, "/argocd/api/v1/applications/demo/sync", nil)
	mutatingReq.Header.Set("Accept", "application/json")
	proxy.ServeHTTP(httptest.NewRecorder(), mutatingReq)

	if len(audit.rows) != 2 {
		t.Fatalf("audit rows = %d, want 2", len(audit.rows))
	}
	if audit.rows[0].Action != "argocd.ui_proxy.opened" {
		t.Fatalf("first audit action = %q", audit.rows[0].Action)
	}
	if audit.rows[1].Action != "argocd.ui_proxy.forwarded" {
		t.Fatalf("second audit action = %q", audit.rows[1].Action)
	}
	if audit.rows[1].ResourceName != "/argocd/api/v1/applications/demo/sync" {
		t.Fatalf("mutating audit resource name = %q", audit.rows[1].ResourceName)
	}
}
