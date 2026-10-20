//go:build integration

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
	"github.com/alphabravocompany/astronomer-go/pkg/logging"
)

// lockedBuffer is the captured log sink; the summary line is written after
// the response reaches the client, so reads poll under the same lock.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type summaryProbe struct {
	name          string
	method        string
	path          string
	body          string
	traceparent   string
	correlationID string
	authenticated bool
	wantRoute     string
	wantStatus    int
}

// TestRequestSummaryLogIntegration drives the production router (real auth
// handler over a migrated Postgres, otelhttp wrapper, production middleware
// chain) and asserts the guide ch.05 request-summary contract on the
// captured zerolog output.
func TestRequestSummaryLogIntegration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	const (
		password       = "Leak-Probe-Password-7f3a"
		wrongPassword  = "Leak-Probe-Wrong-Password-91c2"
		queryToken     = "Leak-Probe-Query-Token-5d10"
		queryPassword  = "Leak-Probe-Query-Password-e44b"
		traceLogin     = "4bf92f3577b34da6a3ce929d0e0e4736"
		traceMe        = "0af7651916cd43dd8448eb211c80319c"
		traceAnonymous = "5b8aa5a2d2c872e8321cf37308d69df2"
	)
	var captured lockedBuffer
	logger, err := logging.NewWithWriter(&captured, logging.Config{Level: "debug", Component: "server"})
	if err != nil {
		t.Fatal(err)
	}
	queries, router := integrationAuthRouter(t, logger, password)
	server := httptest.NewServer(wrapWithTracing(router))
	t.Cleanup(server.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	anonymous := &http.Client{Timeout: 10 * time.Second}

	probes := []summaryProbe{
		{name: "login with traceparent", method: http.MethodPost, path: "/api/v1/auth/login/", body: fmt.Sprintf(`{"email":"admin@example.test","password":%q}`, password), traceparent: "00-" + traceLogin + "-00f067aa0ba902b7-01", wantRoute: "/api/v1/auth/login", wantStatus: http.StatusOK},
		{name: "failed login password in body", method: http.MethodPost, path: "/api/v1/auth/login/", body: fmt.Sprintf(`{"email":"admin@example.test","password":%q}`, wrongPassword), wantRoute: "/api/v1/auth/login", wantStatus: http.StatusUnauthorized},
		{name: "authenticated me with traceparent", method: http.MethodGet, path: "/api/v1/auth/me/", traceparent: "00-" + traceMe + "-b7ad6b7169203331-01", authenticated: true, wantRoute: "/api/v1/auth/me", wantStatus: http.StatusOK},
		{name: "authenticated me with secrets in query", method: http.MethodGet, path: "/api/v1/auth/me/?access_token=" + queryToken + "&password=" + queryPassword, correlationID: "client-correlation-0001", authenticated: true, wantRoute: "/api/v1/auth/me", wantStatus: http.StatusOK},
		{name: "anonymous me with traceparent", method: http.MethodGet, path: "/api/v1/auth/me/", traceparent: "00-" + traceAnonymous + "-a2fb4a1d1a96d312-01", wantRoute: "/api/v1/auth/me", wantStatus: http.StatusUnauthorized},
		{name: "anonymous me", method: http.MethodGet, path: "/api/v1/auth/me/?token=" + queryToken, wantRoute: "/api/v1/auth/me", wantStatus: http.StatusUnauthorized},
		{name: "authenticated me", method: http.MethodGet, path: "/api/v1/auth/me/", authenticated: true, wantRoute: "/api/v1/auth/me", wantStatus: http.StatusOK},
		{name: "login body with secret query", method: http.MethodPost, path: "/api/v1/auth/login/?password=" + queryPassword, body: fmt.Sprintf(`{"email":"nobody@example.test","password":%q}`, wrongPassword), wantRoute: "/api/v1/auth/login", wantStatus: http.StatusUnauthorized},
	}

	correlationByProbe := make([]string, len(probes))
	for i, probe := range probes {
		var body io.Reader
		if probe.body != "" {
			body = strings.NewReader(probe.body)
		}
		req, err := http.NewRequestWithContext(ctx, probe.method, server.URL+probe.path, body)
		if err != nil {
			t.Fatal(err)
		}
		if probe.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if probe.traceparent != "" {
			req.Header.Set("traceparent", probe.traceparent)
		}
		if probe.correlationID != "" {
			req.Header.Set("X-Correlation-ID", probe.correlationID)
		}
		use := anonymous
		if probe.authenticated || probe.wantStatus == http.StatusOK {
			use = client // the successful login stores the session cookie
		}
		resp, err := use.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != probe.wantStatus {
			t.Fatalf("%s: status %d, want %d: %s", probe.name, resp.StatusCode, probe.wantStatus, payload)
		}
		correlationByProbe[i] = resp.Header.Get("X-Correlation-Id")
		if correlationByProbe[i] == "" || resp.Header.Get("X-Request-Id") != correlationByProbe[i] {
			t.Fatalf("%s: correlation id not echoed: %v", probe.name, resp.Header)
		}
	}
	admin, err := queries.GetUserByEmail(ctx, "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	userID := admin.ID.String()

	summaries := waitForSummaries(t, &captured, len(probes))
	for i, probe := range probes {
		id := correlationByProbe[i]
		if want := strings.TrimPrefix(probe.traceparent, "00-"); probe.traceparent != "" && id != want[:32] {
			t.Errorf("%s: correlation_id %q, want traceparent trace-id %q", probe.name, id, want[:32])
		}
		if probe.correlationID != "" && id != probe.correlationID {
			t.Errorf("%s: correlation_id %q, want inbound X-Correlation-ID %q", probe.name, id, probe.correlationID)
		}
		matching := summaries[id]
		if len(matching) != 1 {
			t.Fatalf("%s: %d summary lines for correlation_id %s, want exactly 1", probe.name, len(matching), id)
		}
		line := matching[0]
		if line["route"] != probe.wantRoute || line["method"] != probe.method || line["status"] != float64(probe.wantStatus) || line["level"] != "info" || line["component"] != "server" {
			t.Errorf("%s: summary fields wrong: %v", probe.name, line)
		}
		if _, ok := line["duration_ms"].(float64); !ok {
			t.Errorf("%s: duration_ms missing: %v", probe.name, line)
		}
		principal, hasPrincipal := line["principal"]
		if probe.authenticated && principal != userID {
			t.Errorf("%s: principal %v, want %s", probe.name, principal, userID)
		}
		if !probe.authenticated && hasPrincipal {
			t.Errorf("%s: anonymous request logged principal %v", probe.name, principal)
		}
	}

	all := captured.String()
	for _, secret := range []string{password, wrongPassword, queryToken, queryPassword} {
		if strings.Contains(all, secret) {
			t.Fatalf("secret %q leaked into logs:\n%s", secret, all)
		}
	}
	for _, cookie := range jar.Cookies(mustParseURL(t, server.URL)) {
		if len(cookie.Value) > 16 && strings.Contains(all, cookie.Value) {
			t.Fatalf("session cookie %s leaked into logs", cookie.Name)
		}
	}
	for _, raw := range strings.Split(strings.TrimSpace(all), "\n") {
		t.Log(raw)
	}
}

// waitForSummaries polls until want summary lines exist, asserting every
// captured line is exactly one JSON object.
func waitForSummaries(t *testing.T, captured *lockedBuffer, want int) map[string][]map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		byCorrelation := map[string][]map[string]any{}
		total := 0
		for _, raw := range strings.Split(strings.TrimSpace(captured.String()), "\n") {
			if raw == "" {
				continue
			}
			var line map[string]any
			if err := json.Unmarshal([]byte(raw), &line); err != nil {
				t.Fatalf("log line is not a single JSON object: %q: %v", raw, err)
			}
			if line["event"] != "http_request" {
				continue
			}
			id, _ := line["correlation_id"].(string)
			byCorrelation[id] = append(byCorrelation[id], line)
			total++
		}
		if total >= want || time.Now().After(deadline) {
			if total != want {
				t.Fatalf("captured %d request summary lines, want %d", total, want)
			}
			return byCorrelation
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// integrationAuthRouter builds the production router with the real auth
// handler over a freshly migrated Postgres holding one admin
// (admin@example.test / password).
func integrationAuthRouter(t *testing.T, logger zerolog.Logger, password string) (*sqlc.Queries, http.Handler) {
	t.Helper()
	ctx := t.Context()
	database, err := db.Connect(ctx, testdb.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	queries := sqlc.New(database.Pool())
	if err := auth.EnsureBootstrapAdmin(ctx, queries, auth.BootstrapAdminConfig{
		Username: "admin", Email: "admin@example.test", Password: password,
	}, nil); err != nil {
		t.Fatal(err)
	}
	jwt := auth.MustNewJWTManager("request-summary-integration-signing-key-0001", 60)
	authHandler := handler.NewAuthHandlerWithTokens(queries, queries, jwt)
	authHandler.SetRunTx(sqlcMutationTxRunner[handler.AuthMutationTx](database))
	authHandler.SetPasswordRehasher(queries)
	authHandler.SetRoleBindings(queries)
	authHandler.SetAuditWriter(queries)
	authHandler.SetLockoutQuerier(queries)
	authHandler.SetRevocationQuerier(queries)
	authHandler.SetLogger(logging.Slog(logger))
	return queries, NewRouter(&config.Config{}, RouterDependencies{CoreAuth: CoreAuthDependencies{
		Logger: logger, JWT: jwt, Auth: authHandler, AuthQueries: queries, AuditWriter: queries, Queries: queries,
	}})
}
