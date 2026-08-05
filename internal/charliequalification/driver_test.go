package charliequalification

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type liveState struct {
	mu        sync.Mutex
	feature   bool
	requested string
	authority string
	revision  int64
	emergency bool
	ack       bool
}

type fakeScaler struct{ replicas int }

func (s *fakeScaler) Replicas(context.Context) (int, error) { return s.replicas, nil }
func (s *fakeScaler) Scale(_ context.Context, replicas int) error {
	s.replicas = replicas
	return nil
}
func (s *fakeScaler) WaitReady(_ context.Context, replicas int) error {
	if s.replicas != replicas {
		return fmt.Errorf("replicas = %d", s.replicas)
	}
	return nil
}

func TestZeroScenariosRestoreExactReadOnlyBaseline(t *testing.T) {
	state := &liveState{feature: true, requested: "read_only", authority: "read_only", revision: 41, ack: true}
	server := httptest.NewTLSServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	scaler := &fakeScaler{replicas: 2}
	driver, err := NewLiveDriver(LiveConfig{AstronomerURL: server.URL, AdminToken: "admin", HTTPClient: server.Client(), AgentScaler: scaler})
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"feature_false", "unactivated", "central_disabled", "emergency_disabled"} {
		result := driver.Run(t.Context(), ScenarioRequest{Scenario: scenario, Candidate: Candidate{Version: "v1.2.3"}})
		if !result.Passed {
			t.Fatalf("%s did not pass: %#v", scenario, result)
		}
		state.mu.Lock()
		if !state.feature || state.requested != "read_only" || state.authority != "read_only" || state.emergency || !state.ack {
			t.Fatalf("%s failed to restore exact baseline: %#v", scenario, state)
		}
		state.mu.Unlock()
		if scaler.replicas != 2 {
			t.Fatalf("%s failed to restore agent replicas", scenario)
		}
	}
}

func (s *liveState) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer admin" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/settings/feature.charlie/":
		_, _ = fmt.Fprintf(w, `{"data":{"value":%t,"is_default":false}}`, s.feature)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/admin/settings/feature.charlie/":
		var body struct {
			Value bool `json:"value"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		s.feature = body.Value
		if !body.Value {
			s.emergency = true
			s.requested, s.authority = "disabled", "disabled"
			s.revision++
		}
		_, _ = fmt.Fprintf(w, `{"data":{"value":%t,"is_default":false}}`, s.feature)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/charlie/status/":
		_, _ = fmt.Fprintf(w, `{"data":{"connection":{"connected":true,"disclosure_acknowledged":%t},"mode":{"requested":%q,"authoritative":%q,"revision":%d,"emergency_disabled":%t},"agent":{"agent_version":"v1.2.3","image_digest":"sha256:test"}}}`, s.ack, s.requested, s.authority, s.revision, s.emergency)
	case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/charlie/mode/":
		var body struct {
			Mode      string `json:"mode"`
			Revision  int64  `json:"revision"`
			Emergency bool   `json:"emergency_disable"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Revision != s.revision {
			http.Error(w, "conflict", http.StatusConflict)
			return
		}
		if body.Emergency {
			s.emergency = true
			s.requested, s.authority = "disabled", "disabled"
		} else if s.emergency {
			if body.Mode != "disabled" {
				http.Error(w, "must clear", http.StatusConflict)
				return
			}
			s.emergency = false
		} else {
			s.requested, s.authority = body.Mode, body.Mode
		}
		s.revision++
		_, _ = fmt.Fprintf(w, `{"data":{"requested":%q,"authoritative":%q,"revision":%d,"emergency_disabled":%t}}`, s.requested, s.authority, s.revision, s.emergency)
	default:
		http.NotFound(w, r)
	}
}

func TestCountersParseRequiredRuntimeAndBoundaryFamilies(t *testing.T) {
	lines := []string{}
	for index, name := range defaultCounterMetrics() {
		if contains(runtimeKeys, index) {
			lines = append(lines, fmt.Sprintf("%s %d", name, len(lines)+1))
		}
	}
	lines = append(lines,
		`astronomer_downstream_boundary_calls_total{entrypoint="tunnel_message",operation="other"} 1`,
		`astronomer_downstream_boundary_calls_total{entrypoint="kubernetes_proxy",operation="other"} 2`,
		`astronomer_downstream_boundary_calls_total{entrypoint="other",operation="kubernetes"} 3`,
		`astronomer_downstream_boundary_calls_total{entrypoint="other",operation="exec"} 4`,
		`astronomer_downstream_boundary_calls_total{entrypoint="other",operation="logs"} 5`,
		`astronomer_downstream_boundary_calls_total{entrypoint="other",operation="helm"} 6`)
	metrics := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprintln(w, strings.Join(lines, "\n")) }))
	defer metrics.Close()
	driver, err := NewLiveDriver(LiveConfig{AstronomerURL: metrics.URL, AdminToken: "admin", MetricsURLs: []string{metrics.URL}, HTTPClient: metrics.Client()})
	if err != nil {
		t.Fatal(err)
	}
	value, err := driver.Counters(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !completeCounters(value) || value.Downstream["tunnel"] != 1 || value.Downstream["helm"] != 6 {
		t.Fatalf("unexpected counters: %#v", value)
	}
}

func TestUnsupportedScenarioNeverPasses(t *testing.T) {
	driver := &LiveDriver{}
	result := driver.Run(t.Context(), ScenarioRequest{Scenario: "clean_install"})
	if result.Passed || len(result.Assertions) != 0 {
		t.Fatalf("unsupported scenario passed: %#v", result)
	}
}

func TestSafeOperatorURLAllowsPlainHTTPOnlyForLiteralLoopback(t *testing.T) {
	if _, err := safeOperatorURL("http://127.0.0.1:8000", true); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"http://localhost:8000", "http://127.example.com:8000", "http://0.0.0.0:8000", "http://example.com"} {
		if _, err := safeOperatorURL(raw, true); err == nil {
			t.Fatalf("expected %s to be rejected", raw)
		}
	}
}
