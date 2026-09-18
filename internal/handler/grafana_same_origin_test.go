package handler

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type grafanaAuthenticatedRequesterFake struct {
	clusterID string
	method    string
	path      string
	body      []byte
	headers   map[string]string
	auth      protocol.GrafanaProxyAuth
	response  *protocol.K8sResponsePayload
}

func (f *grafanaAuthenticatedRequesterFake) Do(context.Context, string, string, string, []byte, map[string]string) (*protocol.K8sResponsePayload, error) {
	return nil, nil
}

func (f *grafanaAuthenticatedRequesterFake) DoWithGrafanaAuth(_ context.Context, clusterID, method, path string, body []byte, headers map[string]string, auth protocol.GrafanaProxyAuth) (*protocol.K8sResponsePayload, error) {
	f.clusterID, f.method, f.path, f.body, f.headers, f.auth = clusterID, method, path, body, headers, auth
	return f.response, nil
}

func TestProxyGrafanaUsesSameOriginTunnelAndPathScopedCookie(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
	h.SetGrafanaTickets(auth.NewGrafanaTicketStore(time.Minute))
	if err := h.updateSharedGrafanaMetadata(context.Background(), q.backend, SharedGrafanaRequest{
		ManagementClusterID: stackTestClusterID,
		Namespace:           "monitoring",
		ReleaseName:         sharedGrafanaDefaultRelease,
		ChartVersion:        sharedGrafanaDefaultChart,
	}, "healthy"); err != nil {
		t.Fatal(err)
	}
	fake := &grafanaAuthenticatedRequesterFake{response: &protocol.K8sResponsePayload{
		StatusCode: http.StatusOK,
		Headers: map[string]string{
			"Content-Type": "text/html",
			"Set-Cookie":   protocol.GrafanaProxyCookieName + "=signed.proxy.cookie; Path=/; HttpOnly; Secure",
		},
		Body: base64.StdEncoding.EncodeToString([]byte("<html>grafana</html>")),
	}}
	h.requester = fake

	router := chi.NewRouter()
	router.Handle("/api/v1/observability/grafana/*", http.HandlerFunc(h.ProxyGrafana))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/observability/grafana/d/fleet?orgId=1", nil)
	req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{
		ID: uuid.NewString(), Email: "operator@example.com", AuthMethod: "jwt",
	}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "<html>grafana</html>" {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	if fake.clusterID != stackTestClusterID || fake.method != http.MethodGet {
		t.Fatalf("target = cluster %q method %q", fake.clusterID, fake.method)
	}
	wantPath := "/api/v1/namespaces/monitoring/services/http:" + grafanaProxyServiceName(sharedGrafanaDefaultRelease) + ":8080/proxy/d/fleet?orgId=1"
	if fake.path != wantPath {
		t.Fatalf("path = %q, want %q", fake.path, wantPath)
	}
	if fake.auth.Ticket == "" || fake.auth.Cookie != "" {
		t.Fatalf("first request auth = %+v, want one-use ticket", fake.auth)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("X-Frame-Options = %q", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'self'") {
		t.Fatalf("CSP = %q", got)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != protocol.GrafanaProxyCookieName || cookies[0].Path != sharedGrafanaProxyPath || !cookies[0].HttpOnly {
		t.Fatalf("cookies = %+v", cookies)
	}
}

func TestProxyGrafanaForwardsOnlySignedProxyCookie(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
	h.SetGrafanaTickets(auth.NewGrafanaTicketStore(time.Minute))
	if err := h.updateSharedGrafanaMetadata(context.Background(), q.backend, SharedGrafanaRequest{
		ManagementClusterID: stackTestClusterID, Namespace: "monitoring", ReleaseName: sharedGrafanaDefaultRelease,
	}, "healthy"); err != nil {
		t.Fatal(err)
	}
	fake := &grafanaAuthenticatedRequesterFake{response: &protocol.K8sResponsePayload{StatusCode: http.StatusOK}}
	h.requester = fake
	router := chi.NewRouter()
	router.Handle("/api/v1/observability/grafana/*", http.HandlerFunc(h.ProxyGrafana))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/observability/grafana/", nil)
	req.AddCookie(&http.Cookie{Name: "astronomer_session", Value: "must-not-cross-tunnel"})
	req.AddCookie(&http.Cookie{Name: protocol.GrafanaProxyCookieName, Value: "signed.proxy.cookie"})
	req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: uuid.NewString(), Email: "operator@example.com"}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if fake.auth.Cookie != "signed.proxy.cookie" || fake.auth.Ticket != "" {
		t.Fatalf("auth = %+v", fake.auth)
	}
	if _, ok := fake.headers["Cookie"]; ok {
		t.Fatalf("browser Cookie leaked into ordinary headers: %+v", fake.headers)
	}
}

func TestProxyGrafanaRequiresMonitoringRead(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: denyMonitoring()})
	h.SetGrafanaTickets(auth.NewGrafanaTicketStore(time.Minute))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/observability/grafana/", nil)
	req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{
		ID: uuid.NewString(), Email: "denied@example.com", AuthMethod: "jwt",
	}))
	rec := httptest.NewRecorder()
	h.ProxyGrafana(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

type clusterGrafanaRequesterFake struct {
	paths    []string
	response *protocol.K8sResponsePayload
}

func (f *clusterGrafanaRequesterFake) Do(_ context.Context, _, _, path string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	f.paths = append(f.paths, path)
	if strings.Contains(path, "/services?labelSelector=") {
		body := `{"items":[{"metadata":{"name":"astronomer-monitoring-grafana"},"spec":{"ports":[{"port":80}]}}]}`
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(body))}, nil
	}
	return f.response, nil
}

func TestProxyClusterGrafanaUsesPrivateClusterService(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	q.clusterErr = nil
	q.clusterCfg = sqlc.ClusterMonitoringConfig{
		ClusterID:             uuid.MustParse(stackTestClusterID),
		StackNamespace:        "astronomer-monitoring",
		PrometheusReleaseName: "astronomer-monitoring",
		Status:                "healthy",
	}
	fake := &clusterGrafanaRequesterFake{response: &protocol.K8sResponsePayload{
		StatusCode: http.StatusOK,
		Headers:    map[string]string{"Content-Type": "text/html", "Set-Cookie": "must-not-leak=1"},
		Body:       base64.StdEncoding.EncodeToString([]byte("<html>cluster grafana</html>")),
	}}
	h.requester = fake

	router := chi.NewRouter()
	router.Handle("/api/v1/clusters/{id}/observability/grafana/*", http.HandlerFunc(h.ProxyClusterGrafana))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+stackTestClusterID+"/observability/grafana/d/local?orgId=1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "<html>cluster grafana</html>" {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	if len(fake.paths) != 2 {
		t.Fatalf("paths = %#v, want service discovery plus proxy", fake.paths)
	}
	wantPath := "/api/v1/namespaces/astronomer-monitoring/services/http:astronomer-monitoring-grafana:80/proxy/api/v1/clusters/" + stackTestClusterID + "/observability/grafana/d/local?orgId=1"
	if fake.paths[1] != wantPath {
		t.Fatalf("proxy path = %q, want %q", fake.paths[1], wantPath)
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("cluster Grafana cookie crossed the proxy boundary")
	}
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "script-src 'self' 'unsafe-inline' 'unsafe-eval'") {
		t.Fatalf("CSP = %q", got)
	}
}
