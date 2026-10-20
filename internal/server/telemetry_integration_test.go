//go:build integration

package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/logging"
	"github.com/alphabravocompany/astronomer-go/pkg/telemetry"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

// TestTelemetryMetricsEndpointIntegration boots telemetry.Init (no OTLP
// endpoint) against the process registry, then the production router over a
// migrated Postgres, drives real requests and scrapes the single /metrics
// handler: legacy client_golang families and OTel-exported families (with
// the exporter's target_info resource series) must both be present.
func TestTelemetryMetricsEndpointIntegration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	shutdown, metricsHandler, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName: "astronomer-server", ServiceVersion: version.Version, Environment: "integration",
		ServiceNamespace: "astronomer", ServiceInstanceID: "telemetry-integration",
		Logger: *logging.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shutdown(t.Context()) })

	const password = "Telemetry-Integration-Password-1"
	logger, err := logging.NewWithWriter(io.Discard, logging.Config{Component: "server"})
	if err != nil {
		t.Fatal(err)
	}
	// Built after Init: otelhttp captures the global meter provider.
	_, router := integrationAuthRouter(t, logger, password)
	api := httptest.NewServer(wrapWithTracing(router))
	t.Cleanup(api.Close)
	admin := httptest.NewServer(metricsHandler)
	t.Cleanup(admin.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	requests := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/api/v1/auth/login/", fmt.Sprintf(`{"email":"admin@example.test","password":%q}`, password), http.StatusOK},
		{http.MethodGet, "/api/v1/auth/me/", "", http.StatusOK},
		{http.MethodGet, "/api/v1/auth/me/", "", http.StatusOK},
		{http.MethodPost, "/api/v1/auth/login/", `{"email":"admin@example.test","password":"wrong"}`, http.StatusUnauthorized},
	}
	for _, probe := range requests {
		req, err := http.NewRequestWithContext(ctx, probe.method, api.URL+probe.path, strings.NewReader(probe.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != probe.want {
			t.Fatalf("%s %s: status %d, want %d", probe.method, probe.path, resp.StatusCode, probe.want)
		}
	}

	resp, err := http.Get(admin.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scrape status %d: %s", resp.StatusCode, raw)
	}
	body := string(raw)
	// Legacy collectors are process-global, so parallel tests can add to the
	// counter; OTel series carry this listener's server_port and count exactly.
	checks := map[string]*regexp.Regexp{
		"legacy service_build_info":       regexp.MustCompile(`(?m)^service_build_info\{.*version="` + regexp.QuoteMeta(version.Version) + `".*\} 1$`),
		"legacy astronomer_http_requests": regexp.MustCompile(`(?m)^astronomer_http_requests_total\{.*method="GET".*route_template="/api/v1/auth/me".*status_class="2xx".*\} ([2-9]|\d{2,})$`),
		"otel http.server.request.duration": regexp.MustCompile(
			`(?m)^http_server_request_duration_seconds_count\{.*http_request_method="GET".*otel_scope_name="go\.opentelemetry\.io/contrib/instrumentation/net/http/otelhttp".*\} 2$`),
		"otel target_info resource": regexp.MustCompile(
			`(?m)^target_info\{.*deployment_environment="integration".*service_instance_id="telemetry-integration".*service_name="astronomer-server".*\} 1$`),
	}
	for name, pattern := range checks {
		if !pattern.MatchString(body) {
			t.Errorf("%s: no series matching %s", name, pattern)
		}
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "service_build_info") || strings.HasPrefix(line, "target_info") ||
			strings.HasPrefix(line, "http_server_request_duration_seconds_count") ||
			strings.HasPrefix(line, "astronomer_http_requests_total{") {
			t.Log(line)
		}
	}
}
