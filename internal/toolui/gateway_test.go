package toolui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/google/uuid"
)

func testGateway(t *testing.T, proxy func(http.ResponseWriter, *http.Request, Target)) (*Gateway, string, string) {
	t.Helper()
	manager := auth.MustNewJWTManager("tool-origin-test-signing-key", 15)
	pair, err := manager.PrepareTokenPairContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	access, refresh, err := manager.SignPreparedTokenPair(uuid.New(), pair)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := auth.NewEncryptor("mjc2rBj19lSbsCp4LzOVgAuWBxGJIGsQNc6oZi0iDTQ=")
	if err != nil {
		t.Fatal(err)
	}
	backend, _ := auth.NewToolUIGrantBackend("")
	g, err := New("https://console.example.test", "tools.example.test", manager, cipher, backend, proxy)
	if err != nil {
		t.Fatal(err)
	}
	return g, access, refresh
}

func launchAndRedeem(t *testing.T, g *Gateway, access string, target Target) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest("GET", "https://console.example.test"+target.Prefix()+"explore?query=up", nil)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.AddCookie(&http.Cookie{Name: "__Host-" + auth.SessionCookieName, Value: access})
	w := httptest.NewRecorder()
	g.Launch(w, r, target, target.Prefix()+"explore?query=up")
	if w.Code != 200 {
		t.Fatalf("launch: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), access) {
		t.Fatal("parent JWT leaked into launch")
	}
	grant := regexp.MustCompile(`name="grant" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if len(grant) != 2 {
		t.Fatal("missing launch form")
	}
	redeem := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://"+g.host(target)+redeemPath, strings.NewReader(url.Values{"grant": {grant[1]}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", g.main.String())
		w := httptest.NewRecorder()
		g.Dispatch(http.NotFoundHandler()).ServeHTTP(w, r)
		return w
	}
	w = redeem()
	if w.Code != 303 {
		t.Fatalf("redeem: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Location") != target.Prefix()+"explore?query=up" {
		t.Fatal("deep link lost")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("expected only gateway cookie")
	}
	c := cookies[0]
	if c.Name != CookieName || !c.Secure || !c.HttpOnly || c.Domain != "" || c.Path != "/" {
		t.Fatalf("unsafe cookie attributes: %s", c.Name)
	}
	if repeat := redeem(); repeat.Code != 401 {
		t.Fatal("launch grant replay accepted")
	}
	return c
}

func TestBrowserToolOriginBoundary(t *testing.T) {
	calls := 0
	target := Target{Kind: "grafana"}
	g, access, _ := testGateway(t, func(w http.ResponseWriter, r *http.Request, got Target) {
		calls++
		if got != target || r.Header.Get("Cookie") != "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Fatal("proxy credential boundary")
		}
		w.Header().Set("Set-Cookie", "astronomer_session=attacker; Domain=example.test")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Security-Policy", "default-src * 'unsafe-inline'")
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<script>fetch('/api/v1/auth/me/')</script><a href="https://console.example.test/api/v1/observability/grafana/explore">Explore</a>`))
	})
	cookie := launchAndRedeem(t, g, access, target)
	mainCalls := 0
	dispatch := g.Dispatch(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mainCalls++; w.WriteHeader(204) }))
	for _, path := range []string{"/", "/api/v1/auth/me/", "/internal/tunnel/k8s/x", "/api/v1/clusters/another/proxy/service/ns/svc/"} {
		r := httptest.NewRequest("GET", "https://"+g.host(target)+path, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		dispatch.ServeHTTP(w, r)
		if w.Code != 200 || mainCalls != 0 {
			t.Fatal("tool request reached control-plane router")
		}
		if w.Header().Get("Set-Cookie") != "" || w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Service-Worker-Allowed") != "" {
			t.Fatal("downstream security headers escaped")
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "worker-src blob:") || strings.Contains(w.Body.String(), "https://console.example.test/api/v1/observability/grafana/") {
			t.Fatal("isolation policy or legacy URL rewrite missing")
		}
	}
	before := calls
	for _, tc := range []struct{ method, host, origin, worker string }{
		{"GET", g.host(Target{Kind: "cluster-grafana", Cluster: uuid.NewString()}), "", ""},
		{"POST", g.host(target), "https://console.example.test", ""},
		{"POST", g.host(target), "null", ""},
		{"POST", g.host(target), "", ""},
		{"GET", g.host(target), "", "script"},
	} {
		r := httptest.NewRequest(tc.method, "https://"+tc.host+"/api/test", nil)
		r.AddCookie(cookie)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Service-Worker", tc.worker)
		w := httptest.NewRecorder()
		dispatch.ServeHTTP(w, r)
		if w.Code < 400 || calls != before || mainCalls != 0 {
			t.Fatal("cross-target/origin/worker bypass")
		}
	}
	r := httptest.NewRequest("POST", "https://"+g.host(target)+"/api/ds/query", strings.NewReader(`{}`))
	r.AddCookie(cookie)
	r.Header.Set("Origin", "https://"+g.host(target))
	w := httptest.NewRecorder()
	dispatch.ServeHTTP(w, r)
	if w.Code != 200 || calls != before+1 {
		t.Fatal("legitimate query blocked")
	}
	r = httptest.NewRequest("GET", g.main.String()+"/api/v1/auth/me/", nil)
	r.Header.Set("Origin", "https://"+g.host(target))
	w = httptest.NewRecorder()
	dispatch.ServeHTTP(w, r)
	if w.Code != 403 || mainCalls != 0 {
		t.Fatal("tool-to-main API fetch accepted")
	}
}

func TestToolLaunchRejectsCrossOriginAndRefreshCredentials(t *testing.T) {
	g, access, refresh := testGateway(t, func(http.ResponseWriter, *http.Request, Target) { t.Fatal("unexpected proxy") })
	for _, tc := range []struct{ token, site string }{{access, "same-site"}, {access, "cross-site"}, {refresh, "same-origin"}, {access, ""}} {
		r := httptest.NewRequest("GET", g.main.String()+"/api/v1/observability/grafana/", nil)
		r.AddCookie(&http.Cookie{Name: "__Host-" + auth.SessionCookieName, Value: tc.token})
		r.Header.Set("Sec-Fetch-Site", tc.site)
		w := httptest.NewRecorder()
		g.Launch(w, r, Target{Kind: "grafana"}, "/")
		if w.Code < 400 {
			t.Fatal("unsafe launch accepted")
		}
	}
}

func TestToolSessionScopeAndExpiry(t *testing.T) {
	g, access, _ := testGateway(t, func(http.ResponseWriter, *http.Request, Target) {})
	target := Target{Kind: "grafana"}
	host := g.host(target)
	for _, s := range []session{
		{Purpose: sessionPurpose, Target: target, Host: host, Credential: access, Expires: time.Now().Add(-time.Second)},
		{Purpose: "other-secret", Target: target, Host: host, Credential: access, Expires: time.Now().Add(time.Hour)},
		{Purpose: sessionPurpose, Target: Target{Kind: "cluster-grafana"}, Host: host, Credential: access, Expires: time.Now().Add(time.Hour)},
	} {
		b, _ := json.Marshal(s)
		sealed, _ := g.cipher.Encrypt(string(b))
		if _, err := g.decode(context.Background(), sealed, host); err == nil {
			t.Fatal("invalid session accepted")
		}
	}
}
