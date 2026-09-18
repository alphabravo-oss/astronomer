package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/astrocli"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/spf13/cobra"
)

func authCommand(t *testing.T, server, token string) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "astro"}
	root.PersistentFlags().String("server", "", "")
	root.PersistentFlags().String("token", "", "")
	if server != "" {
		if err := root.PersistentFlags().Set("server", server); err != nil {
			t.Fatal(err)
		}
	}
	if token != "" {
		if err := root.PersistentFlags().Set("token", token); err != nil {
			t.Fatal(err)
		}
	}
	child := &cobra.Command{Use: "child"}
	root.AddCommand(child)
	return child
}

func saveCLIConfig(t *testing.T, cfg *astrocli.Config) {
	t.Helper()
	t.Setenv("ASTRO_CONFIG_HOME", t.TempDir())
	if err := astrocli.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestNewAstroClientInjectsFlagBearerAndUsesServerOverride(t *testing.T) {
	var authorization, requestPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		requestPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"data\":[]}"))
	}))
	defer server.Close()

	saveCLIConfig(t, &astrocli.Config{
		ServerURL:   "https://stored.invalid",
		AccessToken: "stored-token",
	})
	t.Setenv("ASTRO_API_TOKEN", "environment-token")
	cmd := authCommand(t, server.URL, "flag-token")

	client, err := newAstroClient(cmd)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.GetActivity(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	if authorization != "Bearer flag-token" {
		t.Fatalf("authorization = %q", authorization)
	}
	if requestPath != "/api/v1/activity" {
		t.Fatalf("path = %q", requestPath)
	}
}

func TestAuthedClientResolutionFailures(t *testing.T) {
	t.Run("missing server", func(t *testing.T) {
		saveCLIConfig(t, &astrocli.Config{AccessToken: "token"})
		t.Setenv("ASTRO_API_TOKEN", "")
		_, _, err := authedClient(authCommand(t, "", ""))
		if err == nil || !strings.Contains(err.Error(), "no server configured") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("missing token", func(t *testing.T) {
		saveCLIConfig(t, &astrocli.Config{ServerURL: "https://example.invalid"})
		t.Setenv("ASTRO_API_TOKEN", "")
		_, _, err := authedClient(authCommand(t, "", ""))
		if err == nil || !strings.Contains(err.Error(), "not logged in") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestBearerOverridePrecedence(t *testing.T) {
	t.Setenv("ASTRO_API_TOKEN", "environment-token")
	if got := bearerOverride(authCommand(t, "", "")); got != "environment-token" {
		t.Fatalf("environment token = %q", got)
	}
	if got := bearerOverride(authCommand(t, "", "flag-token")); got != "flag-token" {
		t.Fatalf("flag token = %q", got)
	}
}

func TestLoginMintsAPITokenAndLogoutRevokesIt(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ASTRO_CONFIG_HOME", configDir)
	const (
		apiToken   = "astro_cli-token"
		apiTokenID = "292b3f19-e8ae-482f-b065-1d2338ce9391"
		csrfToken  = "csrf-value"
		session    = "http-only-session"
	)
	var revokeCalls atomic.Int32
	var sessionLogoutCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login/":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode login body: %v", err)
			}
			if body["email"] != "admin" || body["password"] != "correct-horse" {
				t.Errorf("login body = %#v", body)
			}
			http.SetCookie(w, &http.Cookie{Name: auth.SessionCookieName, Value: session, Path: "/", HttpOnly: true})
			http.SetCookie(w, &http.Cookie{Name: auth.CSRFCookieName, Value: csrfToken, Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"data\":{\"user\":{\"username\":\"admin\",\"email\":\"admin@example.com\",\"is_superuser\":true}}}"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/tokens/":
			cookie, err := r.Cookie(auth.SessionCookieName)
			if err != nil || cookie.Value != session || r.Header.Get("X-CSRF-Token") != csrfToken {
				t.Error("API token request did not use the CSRF-bound browser session")
			}
			var body struct {
				Name          string   `json:"name"`
				ExpiresInDays int      `json:"expires_in_days"`
				Scopes        []string `json:"scopes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode token body: %v", err)
			}
			if !strings.HasPrefix(body.Name, "astro-cli-") || body.ExpiresInDays != cliTokenLifetimeDays ||
				len(body.Scopes) != 1 || body.Scopes[0] != auth.ScopeAdmin {
				t.Errorf("token body = %#v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"` + apiTokenID + `","token":"` + apiToken + `"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/logout/":
			cookie, err := r.Cookie(auth.SessionCookieName)
			if err != nil || cookie.Value != session || r.Header.Get("X-CSRF-Token") != csrfToken {
				t.Error("browser session cleanup did not use the CSRF-bound session")
			}
			sessionLogoutCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"detail":"Logged out"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/auth/tokens/"+apiTokenID+"/":
			if r.Header.Get("Authorization") != "Bearer "+apiToken {
				t.Errorf("revoke authorization = %q", r.Header.Get("Authorization"))
			}
			revokeCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	login := newLoginCmd()
	var output bytes.Buffer
	login.SetOut(&output)
	login.SetArgs([]string{"--server", server.URL, "--user", "admin", "--password", "correct-horse"})
	if err := login.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Logged in") {
		t.Fatalf("output = %q", output.String())
	}
	cfg, err := astrocli.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerURL != server.URL || cfg.AccessToken != apiToken || cfg.APITokenID != apiTokenID || cfg.RefreshToken != "" || cfg.Username != "admin" {
		t.Fatalf("persisted config = %#v", cfg)
	}
	info, err := os.Stat(filepath.Join(configDir, astrocli.ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}

	logout := newLogoutCmd()
	output.Reset()
	logout.SetOut(&output)
	if err := logout.Execute(); err != nil {
		t.Fatal(err)
	}
	cfg, err = astrocli.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccessToken != "" || cfg.APITokenID != "" || cfg.RefreshToken != "" || cfg.Username != "" {
		t.Fatalf("logout retained credentials: %#v", cfg)
	}
	if revokeCalls.Load() != 1 {
		t.Fatalf("remote revoke calls = %d, want 1", revokeCalls.Load())
	}
	if sessionLogoutCalls.Load() != 1 {
		t.Fatalf("browser session cleanup calls = %d, want 1", sessionLogoutCalls.Load())
	}
}

func TestWhoamiUsesLiveIdentity(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/me/" {
			t.Errorf("path = %q", r.URL.Path)
		}
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"data\":{\"username\":\"live-user\",\"email\":\"live@example.com\",\"is_superuser\":true}}"))
	}))
	defer server.Close()
	saveCLIConfig(t, &astrocli.Config{
		ServerURL:   server.URL,
		AccessToken: "stored-token",
		Username:    "stale-user",
	})

	cmd := newWhoamiCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer stored-token" {
		t.Fatalf("authorization = %q", authorization)
	}
	for _, want := range []string{"live-user", "live@example.com", "true"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output %q missing %q", output.String(), want)
		}
	}
}

func TestLoginRejectsResponseWithoutBrowserSession(t *testing.T) {
	t.Setenv("ASTRO_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"data\":{\"user\":{\"username\":\"admin\"}}}"))
	}))
	defer server.Close()

	cmd := newLoginCmd()
	cmd.SetArgs([]string{"--server", server.URL, "--user", "admin", "--password", "pw"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no CSRF-bound browser session") {
		t.Fatalf("error = %v", err)
	}
}
