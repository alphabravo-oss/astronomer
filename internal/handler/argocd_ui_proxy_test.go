package handler

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const argoProxyCanary = "ARGO_PROXY_CANARY_9d32f1"

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
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		w.Header().Set("Authorization", "Bearer leaked")
		w.Header().Set("Clear-Site-Data", `"cookies"`)
		w.Header().Set("Connection", "upgrade")
		w.Header().Set("Content-Length", "11")
		w.Header().Set("Proxy-Authenticate", "Basic")
		w.Header().Set("Location", "https://user:pass@example.test/path?token="+argoProxyCanary)
		w.Header().Add("Link", "<https://example.test/a?sig="+argoProxyCanary+">; rel=next")
		w.Header().Add("Link", "<https://example.test/b>; rel=prev")
		w.Header()["x-aPi-ToKeN"] = []string{"first-" + argoProxyCanary, "second-" + argoProxyCanary}
		w.Header()["X-Future-Metadata"] = []string{"one", "two"}
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
		"Location",
		"Link",
		"X-Api-Token",
		"X-Future-Metadata",
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
	if rr.Header().Get("Content-Security-Policy") != "default-src 'self'" {
		t.Fatalf("expected Content-Security-Policy to be preserved")
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

func TestSanitizeArgoCDUIResponseHeadersIsCaseInsensitiveAndDefaultDeny(t *testing.T) {
	resp := &http.Response{Header: http.Header{
		"cAcHe-CoNtRoL":  []string{"private", "no-store"},
		"x-aPi-ToKeN":    []string{"one", "two"},
		"lOcAtIoN":       []string{"https://user:pass@example.test?sig=secret"},
		"X-New-Upstream": []string{"future"},
	}}
	sanitizeArgoCDUIResponseHeaders(resp)
	if got := resp.Header.Values("Cache-Control"); len(got) != 2 || got[0] != "private" || got[1] != "no-store" {
		t.Fatalf("allowed multi-value header = %v", got)
	}
	for _, key := range []string{"X-Api-Token", "Location", "X-New-Upstream"} {
		if got := resp.Header.Values(key); len(got) != 0 {
			t.Fatalf("unsafe mixed-case header %s survived: %v", key, got)
		}
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
	if audit.rows[1].ResourceName != "/argocd/api/v1/applications/*/sync" {
		t.Fatalf("mutating audit resource name = %q", audit.rows[1].ResourceName)
	}
}

func TestArgoCDUIProxySanitizesJSONResponsesCompressedAndUncompressed(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "gzip"}[compressed], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				payload := `{"metadata":{"name":"demo"},"spec":{"source":{"repoURL":"https://user:` + argoProxyCanary + `@git.example/team/repo?token=` + argoProxyCanary + `","helm":{"values":"password: ` + argoProxyCanary + `","releaseName":"diagnostic-release"}}},"status":{"health":{"status":"Healthy"},"history":[{"source":{"helm":{"valuesObject":{"password":"` + argoProxyCanary + `"}}}}]}}`
				w.Header().Set("Content-Type", "application/vnd.argoproj.application+json; charset=utf-8")
				w.Header().Set("ETag", `"unsafe-etag"`)
				if compressed {
					var encoded bytes.Buffer
					zw := gzip.NewWriter(&encoded)
					_, _ = zw.Write([]byte(payload))
					_ = zw.Close()
					w.Header().Set("Content-Encoding", "gzip")
					w.Header().Set("Content-Length", fmt.Sprint(encoded.Len()))
					_, _ = w.Write(encoded.Bytes())
					return
				}
				w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
				_, _ = w.Write([]byte(payload))
			}))
			defer upstream.Close()

			proxy, err := NewArgoCDUIProxy(upstream.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/argocd/api/v1/applications/demo", nil)
			req.Header.Set("Accept", "application/json")
			if compressed {
				req.Header.Set("Accept-Encoding", "gzip")
			}
			rr := httptest.NewRecorder()
			proxy.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			if strings.Contains(body, argoProxyCanary) || strings.Contains(body, "user:") {
				t.Fatalf("response leaked canary or URL userinfo: %s", body)
			}
			for _, want := range []string{"Healthy", "diagnostic-release", "https://", "git.example", "/team/repo"} {
				if !strings.Contains(body, want) {
					t.Fatalf("response lost diagnostic %q: %s", want, body)
				}
			}
			for _, header := range []string{"Content-Encoding", "ETag", "Content-MD5", "Digest"} {
				if got := rr.Header().Get(header); got != "" {
					t.Fatalf("stale %s=%q", header, got)
				}
			}
		})
	}
}

func TestArgoCDUIProxyRejectsUnsafeMutationBodiesBeforeUpstream(t *testing.T) {
	for name, tc := range map[string]struct {
		contentType     string
		contentEncoding string
		body            []byte
		wantStatus      int
	}{
		"helm values":          {contentType: "application/json", body: []byte(`{"spec":{"source":{"helm":{"values":"` + argoProxyCanary + `"}}}}`), wantStatus: http.StatusBadRequest},
		"patch wrapper":        {contentType: "application/merge-patch+json", body: []byte(`{"patch":"{\"spec\":{\"source\":{\"helm\":{\"values\":\"` + argoProxyCanary + `\"}}}}"}`), wantStatus: http.StatusBadRequest},
		"json patch path":      {contentType: "application/json-patch+json", body: []byte(`[{"op":"replace","path":"/spec/source/helm/values","value":"` + argoProxyCanary + `"}]`), wantStatus: http.StatusBadRequest},
		"scalar wrapper":       {contentType: "application/json", body: []byte(`"{\"spec\":{\"source\":{\"helm\":{\"values\":\"` + argoProxyCanary + `\"}}}}"`), wantStatus: http.StatusBadRequest},
		"malformed":            {contentType: "application/json", body: []byte(`{"spec":`), wantStatus: http.StatusBadRequest},
		"non json":             {contentType: "text/plain", body: []byte(argoProxyCanary), wantStatus: http.StatusUnsupportedMediaType},
		"gzip":                 {contentType: "application/json", contentEncoding: "gzip", body: gzipTestBytes(t, []byte(`{"spec":{"sources":[{"repoURL":"https://git.example/repo","helm":{"values":"`+argoProxyCanary+`"}}]}}`)), wantStatus: http.StatusBadRequest},
		"unsupported encoding": {contentType: "application/json", contentEncoding: "br", body: []byte(`{"safe":true}`), wantStatus: http.StatusBadRequest},
		"value file traversal": {contentType: "application/json", body: []byte(`{"spec":{"sources":[{"repoURL":"https://charts.example/repo","chart":"platform","helm":{"valueFiles":["$values/../secret.yaml"]}},{"repoURL":"https://git.example/values","ref":"values"}]}}`), wantStatus: http.StatusBadRequest},
		"indexed missing ref":  {contentType: "application/json-patch+json", body: []byte(`[{"op":"add","path":"/spec/sources/0/helm/valueFiles/-","value":"$values/prod.yaml"}]`), wantStatus: http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			upstreamCalls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer upstream.Close()
			var logs bytes.Buffer
			proxy, err := NewArgoCDUIProxy(upstream.URL, slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			if err != nil {
				t.Fatal(err)
			}
			audit := &serviceProxyTestAuditWriter{}
			proxy.SetAuditWriter(audit)
			req := httptest.NewRequest(http.MethodPatch, "/argocd/api/v1/applications/demo", bytes.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			if tc.contentEncoding != "" {
				req.Header.Set("Content-Encoding", tc.contentEncoding)
			}
			rr := httptest.NewRecorder()
			proxy.ServeHTTP(rr, req)
			if rr.Code != tc.wantStatus || upstreamCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", rr.Code, upstreamCalls, rr.Body.String())
			}
			if strings.Contains(logs.String(), argoProxyCanary) {
				t.Fatalf("logs leaked canary: %s", logs.String())
			}
			if len(audit.rows) != 1 {
				t.Fatalf("audit rows=%d, want rejected mutation audit", len(audit.rows))
			}
			auditRaw, _ := json.Marshal(audit.rows)
			if strings.Contains(string(auditRaw), argoProxyCanary) {
				t.Fatalf("audit leaked canary: %s", auditRaw)
			}
		})
	}
}

func TestArgoCDUIProxyRejectsClosedGeneratorUnionViolationsBeforeUpstream(t *testing.T) {
	for name, body := range map[string]string{
		"unknown":         `{"spec":{"generators":[{"plugin":{}}]}}`,
		"casing":          `{"spec":{"generators":[{"List":{"elements":[{"name":"prod"}]}}]}}`,
		"duplicate":       `{"spec":{"generators":[{"list":{"elements":[{"name":"prod"}]},"git":{"repoURL":"https://git.example/repo"}}]}}`,
		"duplicate exact": `{"spec":{"generators":[{"list":{"elements":[{"name":"prod"}]},"list":{"elements":[{"name":"stage"}]}}]}}`,
		"nested secret":   `{"spec":{"generators":[{"matrix":{"generators":[{"list":{"elements":[{"name":"prod"}]}},{"git":{"repoURL":"https://git.example/repo","values":{"note":"apiKey=` + argoProxyCanary + `"}}}]}}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			defer upstream.Close()
			proxy, err := NewArgoCDUIProxy(upstream.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPatch, "/argocd/api/v1/applicationsets/demo", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			proxy.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest || calls != 0 || strings.Contains(rr.Body.String(), argoProxyCanary) {
				t.Fatalf("status=%d calls=%d body=%s", rr.Code, calls, rr.Body.String())
			}
		})
	}
}

func TestArgoCDUIProxyAllowsSchemaScopedProjectWildcardAndSafeValueRepositories(t *testing.T) {
	for name, tc := range map[string]struct {
		path string
		body string
	}{
		"project wildcard and ordinary brackets": {
			path: "/argocd/api/v1/projects/demo",
			body: `{"spec":{"description":"[ordinary maintenance note","destinations":[{"server":"*","namespace":"*"}]}}`,
		},
		"multi-source value repository": {
			path: "/argocd/api/v1/applications/demo",
			body: `{"spec":{"sources":[{"repoURL":"https://charts.example/repo","chart":"platform","helm":{"valueFiles":["$values/prod.yaml","defaults.yaml"]}},{"repoURL":"git@github.com:team/values.git","targetRevision":"main","ref":"values"}]}}`,
		},
		"recursive generator union": {
			path: "/argocd/api/v1/applicationsets/demo",
			body: `{"spec":{"generators":[{"merge":{"mergeKeys":["cluster"],"generators":[{"clusters":{"values":{"tier":"critical"}}},{"matrix":{"generators":[{"git":{"repoURL":"https://git.example/repo","directories":[{"path":"apps/*"}],"values":{"region":"east"}}},{"list":{"elements":[{"cluster":"prod"}]}}]}}]}}]}}`,
		},
		"indexed source patch": {
			path: "/argocd/api/v1/applications/demo",
			body: `[{"op":"add","path":"/spec/sources/-","value":{"repoURL":"https://git.example/values","ref":"values"}},{"op":"add","path":"/spec/sources/-","value":{"repoURL":"https://charts.example/repo","chart":"platform","helm":{"valueFiles":["$values/prod.yaml"]}}},{"op":"replace","path":"/spec/sources/1/targetRevision","value":"main"}]`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer upstream.Close()
			proxy, err := NewArgoCDUIProxy(upstream.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPatch, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			proxy.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK || calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", rr.Code, calls, rr.Body.String())
			}
		})
	}
}

func TestArgoCDUIProxyArbitraryBracketResponsesRoundTripWithoutLogLeak(t *testing.T) {
	for name, message := range map[string]string{
		"malformed credential": `{"token":"` + argoProxyCanary,
		"ordinary brackets":    `[maintenance window`,
	} {
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"message": message})
			}))
			defer upstream.Close()
			var logs bytes.Buffer
			proxy, err := NewArgoCDUIProxy(upstream.URL, slog.New(slog.NewJSONHandler(&logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			proxy.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/argocd/api/v1/applications", nil))
			if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), argoProxyCanary) {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			if name == "ordinary brackets" && !strings.Contains(rr.Body.String(), "[maintenance window") {
				t.Fatalf("ordinary description did not round-trip: %s", rr.Body.String())
			}
			if strings.Contains(logs.String(), argoProxyCanary) {
				t.Fatalf("proxy log leaked canary: %s", logs.String())
			}
		})
	}
}

func TestArgoCDUIProxyRejectsAPIProtocolUpgradeBeforeUpstream(t *testing.T) {
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls++
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	defer upstream.Close()
	proxy, err := NewArgoCDUIProxy(upstream.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/argocd/api/v1/applications?watch=true", nil)
	req.Header.Set("Connection", "keep-alive, Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden || upstreamCalls != 0 {
		t.Fatalf("status=%d calls=%d", rr.Code, upstreamCalls)
	}
}

func TestArgoCDUIProxyForwardsSafeJSONMutationAndNonAPIAsset(t *testing.T) {
	for _, tc := range []struct {
		method, path, contentType, body string
	}{
		{method: http.MethodPost, path: "/argocd/api/v1/applications/demo/sync", contentType: "application/json", body: `{"name":"demo","revision":"main","prune":false}`},
		{method: http.MethodGet, path: "/argocd/assets/main.js", contentType: "application/javascript", body: "window.ARGO_DIAGNOSTIC=true;"},
	} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", tc.contentType)
			_, _ = w.Write([]byte(tc.body))
		}))
		proxy, err := NewArgoCDUIProxy(upstream.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		if tc.method != http.MethodGet {
			req.Header.Set("Content-Type", tc.contentType)
		}
		rr := httptest.NewRecorder()
		proxy.ServeHTTP(rr, req)
		upstream.Close()
		if rr.Code != http.StatusOK {
			t.Fatalf("method=%s status=%d body=%q", tc.method, rr.Code, rr.Body.String())
		}
		if tc.method == http.MethodGet && rr.Body.String() != tc.body {
			t.Fatalf("stream changed: %q", rr.Body.String())
		}
		if tc.method != http.MethodGet {
			var got, want any
			if json.Unmarshal(rr.Body.Bytes(), &got) != nil || json.Unmarshal([]byte(tc.body), &want) != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("safe JSON mutation response changed semantically: %s", rr.Body.String())
			}
		}
	}
}

func TestArgoCDUIProxyFailsClosedForSensitiveNonJSONAPIStreams(t *testing.T) {
	for name, tc := range map[string]struct {
		path, contentType, body string
	}{
		"application sse":  {path: "/argocd/api/v1/stream/applications", contentType: "text/event-stream", body: "data: " + argoProxyCanary + "\n\n"},
		"operation ndjson": {path: "/argocd/api/v1/stream/applications/demo", contentType: "application/x-ndjson", body: `{"source":{"helm":{"values":"` + argoProxyCanary + `"}}}` + "\n"},
		"pod logs":         {path: "/argocd/api/v1/applications/demo/pods/pod/logs", contentType: "text/plain", body: "token=" + argoProxyCanary + "\n"},
	} {
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			proxy, _ := NewArgoCDUIProxy(upstream.URL, nil)
			rr := httptest.NewRecorder()
			proxy.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rr.Code != http.StatusBadGateway || strings.Contains(rr.Body.String(), argoProxyCanary) {
				t.Fatalf("stream status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestArgoCDUIProxyMalformedJSONResponseFailsClosed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"secret":"` + argoProxyCanary))
	}))
	defer upstream.Close()
	proxy, _ := NewArgoCDUIProxy(upstream.URL, nil)
	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/argocd/api/v1/applications", nil))
	if rr.Code != http.StatusBadGateway || strings.Contains(rr.Body.String(), argoProxyCanary) {
		t.Fatalf("malformed response status=%d body=%s", rr.Code, rr.Body.String())
	}
}

type staticArgoTokenSource string

func (s staticArgoTokenSource) UpstreamSessionToken(context.Context) (string, error) {
	return string(s), nil
}

type failingArgoTokenSource string

func (s failingArgoTokenSource) UpstreamSessionToken(context.Context) (string, error) {
	return "", fmt.Errorf("token=%s", string(s))
}

func TestArgoCDUIProxyNeverLogsTokenFragments(t *testing.T) {
	const tokenCanary = "ARGO_TOKEN_FRAGMENT_CANARY_d4e5f6"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"health":"Healthy"}`))
	}))
	defer upstream.Close()
	var logs bytes.Buffer
	proxy, _ := NewArgoCDUIProxy(upstream.URL, slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	proxy.SetSessionTokenSource(staticArgoTokenSource(tokenCanary))
	proxy.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/argocd/api/v1/applications", nil))
	proxy.SetSessionTokenSource(failingArgoTokenSource(tokenCanary))
	proxy.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/argocd/api/v1/applications", nil))
	if strings.Contains(logs.String(), tokenCanary) || strings.Contains(logs.String(), "token_prefix") {
		t.Fatalf("logs contain token fragment: %s", logs.String())
	}
}

func TestSafeArgoCDProxyPathNeverRetainsDynamicCredentialFragments(t *testing.T) {
	for _, path := range []string{
		"/argocd/api/v1/repositories/https:%2F%2Fuser:" + argoProxyCanary + "@git.example%2Frepo/validate",
		"/argocd/api/v1/applications/" + argoProxyCanary + "/sync",
		"/argocd/api/v1/unknown/" + argoProxyCanary,
	} {
		if got := safeArgoCDProxyPath(path); strings.Contains(got, argoProxyCanary) || strings.Contains(got, "user:") {
			t.Fatalf("safe path %q retained credential fragment from %q", got, path)
		}
	}
	if got := safeArgoCDProxyPath("/argocd/api/v1/applications/demo/sync"); got != "/argocd/api/v1/applications/*/sync" {
		t.Fatalf("application path family=%q", got)
	}
}

func gzipTestBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
