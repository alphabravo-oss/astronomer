package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/toolui"
	"github.com/google/uuid"
)

func TestIsolatedToolRouterPreservesRBACAndCannotReachProductAPI(t *testing.T) {
	mgr := auth.MustNewJWTManager("tool-router-test-signing-secret", 15)
	pair, _ := mgr.PrepareTokenPairContext(context.Background())
	access, _, err := mgr.SignPreparedTokenPair(uuid.New(), pair)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := auth.NewEncryptor("mjc2rBj19lSbsCp4LzOVgAuWBxGJIGsQNc6oZi0iDTQ=")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ServerURL: "https://console.example.test", ToolUIDomain: "tools.example.test"}
	deps := RouterDependencies{CoreAuth: CoreAuthDependencies{JWT: mgr, Encryptor: cipher, RBACEngine: rbac.NewEngine(), RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceServices, rbac.VerbProxy)}}, ClusterResources: ClusterResourceDependencies{ServiceProxy: routeSecurityServiceProxy()}}
	router := NewRouter(cfg, deps)
	target := toolui.Target{Kind: "service", Cluster: uuid.NewString(), Namespace: "observability", ServicePort: "grafana:3000"}
	launch := httptest.NewRequest("GET", cfg.ServerURL+target.Prefix(), nil)
	launch.Header.Set("Sec-Fetch-Site", "same-origin")
	launch.AddCookie(&http.Cookie{Name: "__Host-" + auth.SessionCookieName, Value: access})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, launch)
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>fetch") {
		t.Fatalf("launch=%d %s", w.Code, w.Body.String())
	}
	grant := regexp.MustCompile(`name="grant" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if len(grant) != 2 {
		t.Fatal("no isolated launch form")
	}
	host := toolui.Host(cfg.ToolUIDomain, target)
	redeem := httptest.NewRequest("POST", "https://"+host+"/.astronomer/launch", strings.NewReader(url.Values{"grant": {grant[1]}}.Encode()))
	redeem.Header.Set("Origin", cfg.ServerURL)
	redeem.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, redeem)
	if w.Code != 303 {
		t.Fatalf("redeem=%d %s", w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	for _, path := range []string{target.Prefix(), "/api/v1/auth/login/", "/internal/tunnel/k8s/", "/api/v1/health"} {
		r := httptest.NewRequest("GET", "https://"+host+path, nil)
		r.AddCookie(cookie)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Fatalf("tool path %s = %d %s", path, w.Code, w.Body.String())
		}
	}
	// A sibling replica with changed RBAC must reject the same scoped cookie.
	deps.CoreAuth.RBACQueries = routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead)}
	revoked := NewRouter(cfg, deps)
	r := httptest.NewRequest("GET", "https://"+host+"/", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	revoked.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("revoked permission = %d", w.Code)
	}
	// Main-host legacy paths can never execute a downstream body, even with a
	// valid API credential and permissions.
	r = httptest.NewRequest("POST", cfg.ServerURL+target.Prefix(), nil)
	r.Header.Set("Authorization", "Bearer "+access)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 410 {
		t.Fatalf("main mutation = %d", w.Code)
	}
}
