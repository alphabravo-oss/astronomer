package argocd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "test-token", Options{HTTPClient: srv.Client(), Timeout: 5 * time.Second})
	return c, srv
}

func TestSyncSuccess(t *testing.T) {
	var seenAuth, seenBody string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("want POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/applications/myapp/sync" {
			t.Errorf("want sync path, got %s", r.URL.Path)
		}
		seenAuth = r.Header.Get("Authorization")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body)
		seenBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"metadata": {"name": "myapp"},
			"status": {
				"sync": {"status": "OutOfSync", "revision": "abc123"},
				"health": {"status": "Healthy"},
				"operationState": {
					"phase": "Running",
					"message": "syncing",
					"startedAt": "2026-05-08T12:00:00Z"
				}
			}
		}`))
	})

	app, err := c.Sync(context.Background(), "myapp", SyncOptions{Revision: "main", Prune: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if app.Status.OperationState == nil || app.Status.OperationState.Phase != "Running" {
		t.Fatalf("expected Running phase, got %+v", app.Status.OperationState)
	}
	if seenAuth != "Bearer test-token" {
		t.Errorf("auth header = %q", seenAuth)
	}
	if !strings.Contains(seenBody, `"prune":true`) || !strings.Contains(seenBody, `"revision":"main"`) {
		t.Errorf("body = %s; missing expected fields", seenBody)
	}
}

func TestSyncUnauthorized(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": "invalid token"}`))
	})
	_, err := c.Sync(context.Background(), "any", SyncOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsKind(err, ErrUnauthorized) {
		t.Errorf("want ErrUnauthorized, got %v", err)
	}
}

func TestSyncNotFound(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error": "no such app"}`))
	})
	_, err := c.Sync(context.Background(), "ghost", SyncOptions{})
	if !IsKind(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestSyncServerError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": "boom"}`))
	})
	_, err := c.Sync(context.Background(), "myapp", SyncOptions{})
	if !IsKind(err, ErrServer) {
		t.Errorf("want ErrServer, got %v", err)
	}
}

func TestTypedResponseBodyLimitRejectsSuccessAndErrorWithoutEcho(t *testing.T) {
	const canary = "ARGO_TYPED_LIMIT_CANARY_51b6"
	for _, tc := range []struct {
		name   string
		status int
		kind   ErrorKind
	}{
		{name: "success", status: http.StatusOK, kind: ErrUnknown},
		{name: "error", status: http.StatusInternalServerError, kind: ErrServer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(canary))
				_, _ = w.Write([]byte(strings.Repeat("x", 16<<20)))
			})
			_, err := c.GetApp(context.Background(), "large")
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %T %v, want APIError", err, err)
			}
			if apiErr.Kind != tc.kind || apiErr.Message != responseBodyLimitMessage || apiErr.Body != "" {
				t.Fatalf("APIError = %+v", apiErr)
			}
			if strings.Contains(apiErr.Error(), canary) || strings.Contains(PublicErrorMessage(apiErr), canary) {
				t.Fatal("over-limit response echoed upstream content")
			}
		})
	}
}

func TestGetAppOperationState(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/applications/myapp" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"metadata": {"name": "myapp"},
			"status": {
				"operationState": {
					"phase": "Succeeded",
					"finishedAt": "2026-05-08T12:01:00Z",
					"syncResult": {"revision": "deadbeef"}
				}
			}
		}`))
	})
	app, err := c.GetApp(context.Background(), "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if app.Status.OperationState.Phase != "Succeeded" {
		t.Errorf("phase = %s", app.Status.OperationState.Phase)
	}
	if app.Status.OperationState.SyncResult == nil || app.Status.OperationState.SyncResult.Revision != "deadbeef" {
		t.Errorf("missing syncResult.revision")
	}
}

func TestRefreshHard(t *testing.T) {
	var seenQuery string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"metadata": {"name": "myapp"}, "status": {}}`))
	})
	if _, err := c.Refresh(context.Background(), "myapp", true); err != nil {
		t.Fatal(err)
	}
	if seenQuery != "refresh=hard" {
		t.Errorf("query = %q", seenQuery)
	}
}

func TestHealth(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"Version": "v2.10.0"}`))
	})
	st, err := c.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != "v2.10.0" {
		t.Errorf("version = %s", st.Version)
	}
}

func TestUnreachable(t *testing.T) {
	// Point the client at a closed local port. We don't need a server.
	c := NewClient("http://127.0.0.1:1", "tok", Options{Timeout: 200 * time.Millisecond})
	_, err := c.GetApp(context.Background(), "any")
	if !IsKind(err, ErrUnreachable) {
		t.Errorf("want ErrUnreachable, got %v", err)
	}
}

func TestArgoCDPathFamily(t *testing.T) {
	cases := map[string]string{
		"/api/v1/applications/my-app/sync":                       "/api/v1/applications/*/sync",
		"/api/v1/applications/my-app":                            "/api/v1/applications/*",
		"/api/v1/applications/my-app?refresh=hard":               "/api/v1/applications/*",
		"/api/v1/applicationsets/platform":                       "/api/v1/applicationsets/*",
		"/api/v1/projects/platform":                              "/api/v1/projects/*",
		"/api/v1/clusters/https:%2F%2Fkubernetes.default.svc":    "/api/v1/clusters/*",
		"/api/v1/repositories/https:%2F%2Fgithub.com%2Frepo.git": "/api/v1/repositories/*",
		"/api/version": "/api/version",
	}
	for in, want := range cases {
		if got := argoCDPathFamily(in); got != want {
			t.Fatalf("argoCDPathFamily(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArgoCDMetricStatus(t *testing.T) {
	if got := argocdMetricStatus(http.StatusOK, nil); got != "200" {
		t.Fatalf("success status = %q", got)
	}
	if got := argocdMetricStatus(0, context.DeadlineExceeded); got != "error" {
		t.Fatalf("error status = %q", got)
	}
	if got := argocdMetricStatus(0, nil); got != "unknown" {
		t.Fatalf("unknown status = %q", got)
	}
}
