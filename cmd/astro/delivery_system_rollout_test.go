package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/astrocli"
)

const (
	systemTestReleaseID = "6f1c1b1e-6a59-4d3c-9d43-0b1a8f6c0001"
	systemTestRolloutID = "6f1c1b1e-6a59-4d3c-9d43-0b1a8f6c0002"
)

// fakeSystemRolloutAPI models the three endpoints the post-upgrade step uses.
type fakeSystemRolloutAPI struct {
	t        *testing.T
	mu       sync.Mutex
	started  map[string]any
	state    string
	fence    int
	requests []string
}

func (f *fakeSystemRolloutAPI) rollout() map[string]any {
	return map[string]any{"id": systemTestRolloutID, "release_id": systemTestReleaseID, "state": f.state, "fencing_generation": f.fence}
}

func (f *fakeSystemRolloutAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if got := r.Header.Get("Authorization"); got != "Bearer isolated-token" {
		f.t.Errorf("Authorization = %q", got)
	}
	write := func(status int, data any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}
	switch r.Method + " " + r.URL.Path {
	case "GET " + systemCompatibilityPath:
		data := map[string]any{
			"current_release": map[string]any{"id": "old", "version": "v2.0.0", "state": "released"},
			"pending_release": map[string]any{"id": systemTestReleaseID, "version": "v2.0.1", "state": "draft"},
			"current_rollout": nil,
		}
		if f.started != nil {
			data["current_rollout"] = f.rollout()
		}
		write(http.StatusOK, data)
	case "POST " + systemRolloutsPath:
		if r.Header.Get("Idempotency-Key") == "" {
			f.t.Error("start without Idempotency-Key")
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &f.started); err != nil {
			f.t.Errorf("start body %s: %v", body, err)
		}
		f.state, f.fence = "awaiting_approval", 1
		write(http.StatusAccepted, f.rollout())
	case "GET " + systemRolloutsPath + systemTestRolloutID + "/":
		write(http.StatusOK, f.rollout())
	case "POST " + systemRolloutsPath + systemTestRolloutID + "/approve/":
		if got := r.Header.Get("If-Match"); got != `"1"` {
			f.t.Errorf("approve If-Match = %q, want \"1\"", got)
		}
		if r.Header.Get("Idempotency-Key") == "" {
			f.t.Error("approve without Idempotency-Key")
		}
		f.state, f.fence = "progressing", 2
		write(http.StatusOK, f.rollout())
	case "GET " + systemRolloutsPath + systemTestRolloutID + "/clusters/":
		write(http.StatusOK, []map[string]any{{"cluster_id": "c1", "phase": "released"}})
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func runSystemRolloutCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append(append([]string{"delivery", "system-rollout"}, args...), "--output=json"))
	err := root.Execute()
	return out.String(), err
}

func TestSystemRolloutStartApproveStatusAgainstAPI(t *testing.T) {
	api := &fakeSystemRolloutAPI{t: t}
	server := httptest.NewServer(api)
	defer server.Close()
	saveCLIConfig(t, &astrocli.Config{ServerURL: server.URL, AccessToken: "isolated-token"})
	t.Setenv("ASTRO_API_TOKEN", "")

	out, err := runSystemRolloutCLI(t, "start")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if api.started["release_id"] != systemTestReleaseID {
		t.Fatalf("start promoted %v, want the pending release", api.started["release_id"])
	}
	strategy, _ := api.started["strategy"].(map[string]any)
	unavailable, _ := strategy["max_unavailable"].(map[string]any)
	if strategy["type"] != "rolling" || strategy["max_concurrent"] != float64(1) || unavailable["value"] != float64(1) {
		t.Fatalf("default strategy = %v", strategy)
	}
	if !strings.Contains(out, systemTestRolloutID) {
		t.Fatalf("start output missing rollout id: %s", out)
	}

	if _, err := runSystemRolloutCLI(t, "approve"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	out, err = runSystemRolloutCLI(t, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var status map[string]map[string]any
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("status output %q: %v", out, err)
	}
	if status["pending_release"]["id"] != systemTestReleaseID || status["current_rollout"]["state"] != "progressing" {
		t.Fatalf("status = %v", status)
	}
	if out, err := runSystemRolloutCLI(t, "status", systemTestRolloutID, "--clusters"); err != nil || !strings.Contains(out, "c1") {
		t.Fatalf("status --clusters: %v %s", err, out)
	}

	want := []string{
		"GET " + systemCompatibilityPath, "POST " + systemRolloutsPath,
		"GET " + systemCompatibilityPath, "GET " + systemRolloutsPath + systemTestRolloutID + "/",
		"POST " + systemRolloutsPath + systemTestRolloutID + "/approve/",
		"GET " + systemCompatibilityPath,
		"GET " + systemRolloutsPath + systemTestRolloutID + "/clusters/",
	}
	if strings.Join(api.requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(api.requests, "\n"), strings.Join(want, "\n"))
	}
}

func TestSystemRolloutStartWithoutPendingReleaseFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"current_release":{"id":"x"},"pending_release":null,"current_rollout":null}}`)
	}))
	defer server.Close()
	saveCLIConfig(t, &astrocli.Config{ServerURL: server.URL, AccessToken: "isolated-token"})
	t.Setenv("ASTRO_API_TOKEN", "")
	if _, err := runSystemRolloutCLI(t, "start"); err == nil || !strings.Contains(err.Error(), "no pending delivery-system release") {
		t.Fatalf("err = %v", err)
	}
	if _, err := runSystemRolloutCLI(t, "approve"); err == nil || !strings.Contains(err.Error(), "no current system rollout") {
		t.Fatalf("err = %v", err)
	}
}
