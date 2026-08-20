package grafanaproxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testProxy(t *testing.T, upstream http.Handler, redeem func(string) (redeemResult, error)) (*proxy, []byte) {
	t.Helper()
	key := []byte("0123456789abcdef0123456789abcdef")
	var upURL *url.URL
	if upstream != nil {
		srv := httptest.NewServer(upstream)
		t.Cleanup(srv.Close)
		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		upURL = u
	} else {
		upURL, _ = url.Parse("http://127.0.0.1:9")
	}
	cfg := Config{
		ListenAddr:    ":0",
		Upstream:      upURL,
		AstronomerURL: "https://astronomer.example.com",
		GrafanaHost:   "grafana.example.com",
		HMACKey:       key,
		Redeem:        redeem,
		Now:           time.Now,
	}
	h := New(cfg)
	p, ok := h.(*proxy)
	if !ok {
		t.Fatalf("handler type %T", h)
	}
	return p, key
}

func viewerCookie(t *testing.T, key []byte) string {
	t.Helper()
	signed, err := signGrafanaAuth(key, grafanaAuth{
		Email: "viewer@example.com", Role: "Viewer", Explore: false, Admin: false,
		Exp: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func adminCookie(t *testing.T, key []byte) string {
	t.Helper()
	signed, err := signGrafanaAuth(key, grafanaAuth{
		Email: "admin@example.com", Role: "Admin", Explore: true, Admin: true,
		Exp: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestViewerExploreForbidden(t *testing.T) {
	p, key := testProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream should not be called for viewer explore, path=%s", r.URL.Path)
	}), nil)
	for _, path := range []string{"/explore", "/explore/", "/explore/foo"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: grafanaAuthCookie, Value: viewerCookie(t, key)})
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403", path, rec.Code)
		}
	}
}

func TestViewerDatasourceQueryAllowed(t *testing.T) {
	var sawDashboardUID bool
	p, key := testProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Dashboard-Uid") != "" {
			sawDashboardUID = true
		}
		if r.Header.Get("X-WEBAUTH-USER") != "viewer@example.com" {
			t.Errorf("X-WEBAUTH-USER = %q", r.Header.Get("X-WEBAUTH-USER"))
		}
		if r.URL.Path != "/api/ds/query" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/ds/query", strings.NewReader(`{"queries":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: grafanaAuthCookie, Value: viewerCookie(t, key)})
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if sawDashboardUID {
		t.Fatal("must not gate or require X-Dashboard-Uid")
	}
}

func TestAdminExploreAllowed(t *testing.T) {
	p, key := testProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/explore" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("explore"))
	}), nil)
	req := httptest.NewRequest(http.MethodGet, "/explore", nil)
	req.AddCookie(&http.Cookie{Name: grafanaAuthCookie, Value: adminCookie(t, key)})
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestViewerDatasourceProxyAllowedCreateForbidden(t *testing.T) {
	p, key := testProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), nil)
	cookie := viewerCookie(t, key)
	for _, path := range []string{
		"/api/datasources/proxy/1/api/v1/query_range",
		"/api/datasources/uid/thanos/resources/api/v1/query",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.AddCookie(&http.Cookie{Name: grafanaAuthCookie, Value: cookie})
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rec.Code)
		}
	}
	create := httptest.NewRequest(http.MethodPost, "/api/datasources", strings.NewReader(`{"name":"x"}`))
	create.AddCookie(&http.Cookie{Name: grafanaAuthCookie, Value: cookie})
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, create)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /api/datasources status = %d, want 403", rec.Code)
	}
}

func TestViewerAdminAPIForbidden(t *testing.T) {
	p, key := testProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called for viewer /api/admin")
	}), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/admin", nil)
	req.AddCookie(&http.Cookie{Name: grafanaAuthCookie, Value: viewerCookie(t, key)})
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestMissingCookieRedirectsToMint(t *testing.T) {
	p, _ := testProxy(t, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "/api/v1/observability/grafana-ticket?return=") {
		t.Fatalf("location = %s", loc)
	}
	if strings.Contains(loc, "ticket=") {
		t.Fatal("mint redirect must not include a ticket")
	}
}

func TestCallbackCookieSecureFollowsAstronomerScheme(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	up, _ := url.Parse("http://127.0.0.1:9")
	h := New(Config{
		Upstream:      up,
		AstronomerURL: "http://astronomer.example.com",
		GrafanaHost:   "grafana.example.com",
		HMACKey:       key,
		Redeem: func(string) (redeemResult, error) {
			return redeemResult{Email: "a@example.com", Role: "Viewer", TTL: 60}, nil
		},
		Now: time.Now,
	})
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?ticket=x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == grafanaAuthCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("missing cookie")
	}
	if cookie.Secure {
		t.Fatal("Secure cookie on http Astronomer URL")
	}
}

func TestCallbackRedeemsAndSetsHostOnlyCookie(t *testing.T) {
	p, key := testProxy(t, nil, func(ticket string) (redeemResult, error) {
		if ticket != "one-use" {
			t.Fatalf("ticket = %q", ticket)
		}
		return redeemResult{Email: "a@example.com", Role: "Viewer", TTL: 3600}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?ticket=one-use", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == grafanaAuthCookie {
			cookie = c
		}
		if c.Name == "astronomer_session" {
			t.Fatal("must not copy astronomer_session")
		}
	}
	if cookie == nil {
		t.Fatal("missing grafana_auth cookie")
	}
	if cookie.Domain != "" {
		t.Fatalf("grafana_auth Domain = %q, want host-only empty", cookie.Domain)
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("cookie attrs = %+v", cookie)
	}
	auth, err := verifyGrafanaAuth(key, cookie.Value)
	if err != nil || auth.Email != "a@example.com" {
		t.Fatalf("verify = %+v err=%v", auth, err)
	}
}

func TestCallbackPersistsClusterIDs(t *testing.T) {
	ids := []string{testClusterA}
	p, key := testProxy(t, nil, func(string) (redeemResult, error) {
		return redeemResult{Email: "scoped@example.com", Role: "Viewer", TTL: 60, Explore: true, ClusterIDs: ids}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?ticket=x", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == grafanaAuthCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("missing cookie")
	}
	auth, err := verifyGrafanaAuth(key, cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.Explore || len(auth.ClusterIDs) != 1 || auth.ClusterIDs[0] != testClusterA {
		t.Fatalf("auth = %+v", auth)
	}
}

func TestConfigFromEnvRejectsMissing(t *testing.T) {
	t.Setenv("GRAFANA_UPSTREAM", "")
	t.Setenv("ASTRONOMER_URL", "")
	t.Setenv("GRAFANA_HOST", "")
	t.Setenv("GRAFANA_PROXY_KEY", "")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error")
	}
}

func TestProxyStripsGrafanaSetCookie(t *testing.T) {
	p, key := testProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "grafana_session", Value: "admin"})
		w.WriteHeader(http.StatusOK)
	}), nil)
	req := httptest.NewRequest(http.MethodGet, "/d/x", nil)
	req.AddCookie(&http.Cookie{Name: grafanaAuthCookie, Value: viewerCookie(t, key)})
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "grafana_session" {
			t.Fatal("grafana Set-Cookie must be stripped")
		}
	}
}

func TestRedeemHTTPUsesTicketOnly(t *testing.T) {
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			sawAuth = true
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["ticket"] != "abc" {
			t.Errorf("body = %v", body)
		}
		_, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"email": "a@example.com", "role": "Viewer", "ttl": 60},
		})
	}))
	t.Cleanup(srv.Close)
	cfg := Config{AstronomerURL: srv.URL}
	got, err := cfg.redeemHTTP("abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "a@example.com" || sawAuth {
		t.Fatalf("got=%+v sawAuth=%v", got, sawAuth)
	}
}
