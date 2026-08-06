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
	mu                        sync.Mutex
	feature                   bool
	requested                 string
	authority                 string
	revision                  int64
	emergency                 bool
	ack                       bool
	disclosureDigest          string
	desiredReplicas           int32
	readyReplicas             int32
	runtime                   map[string]uint64
	downstream                map[string]uint64
	acknowledgedDigests       []string
	incrementOnFeatureRestore bool
	ignoreAcknowledgement     bool
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
	state := newLiveState()
	server := httptest.NewTLSServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	scaler := &fakeScaler{replicas: 2}
	driver, err := NewLiveDriver(LiveConfig{AstronomerURL: server.URL, AdminToken: "admin", MetricSources: []MetricSource{{URL: server.URL + "/metrics", Token: "admin"}}, HTTPClient: server.Client(), AgentScaler: scaler})
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"feature_false", "unactivated", "central_disabled", "emergency_disabled"} {
		result := driver.Run(t.Context(), ScenarioRequest{Scenario: scenario, Candidate: Candidate{Version: "v1.2.3"}})
		if !result.Passed {
			t.Fatalf("%s did not pass: %#v", scenario, result)
		}
		state.mu.Lock()
		if !state.feature || state.requested != "read_only" || state.authority != "read_only" || state.emergency || !state.ack || state.disclosureDigest != readOnlyDisclosureDigest {
			t.Fatalf("%s failed to restore exact baseline: %#v", scenario, state)
		}
		if len(state.acknowledgedDigests) == 0 || state.acknowledgedDigests[len(state.acknowledgedDigests)-1] != readOnlyDisclosureDigest {
			t.Fatalf("%s did not acknowledge the exact baseline digest: %#v", scenario, state.acknowledgedDigests)
		}
		state.mu.Unlock()
		if scaler.replicas != 2 {
			t.Fatalf("%s failed to restore agent replicas", scenario)
		}
	}
}

const (
	readOnlyDisclosureDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	disabledDisclosureDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

func newLiveState() *liveState {
	return &liveState{
		feature: true, requested: "read_only", authority: "read_only", revision: 41, ack: true,
		disclosureDigest: readOnlyDisclosureDigest, desiredReplicas: 2, readyReplicas: 2,
		runtime: map[string]uint64{}, downstream: map[string]uint64{},
	}
}

func TestCleanupFailsWhenCountersDoNotReturnToBaseline(t *testing.T) {
	state := newLiveState()
	state.incrementOnFeatureRestore = true
	server := httptest.NewTLSServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	driver, err := NewLiveDriver(LiveConfig{AstronomerURL: server.URL, AdminToken: "admin", MetricSources: []MetricSource{{URL: server.URL + "/metrics", Token: "admin"}}, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	result := driver.Run(t.Context(), ScenarioRequest{Scenario: "feature_false"})
	if result.Passed {
		t.Fatalf("scenario passed despite cleanup counter drift: %#v", result)
	}
}

func TestRestoreModeReacknowledgesExactDigestAfterModeDigestDrift(t *testing.T) {
	state := newLiveState()
	server := httptest.NewTLSServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	driver, err := NewLiveDriver(LiveConfig{AstronomerURL: server.URL, AdminToken: "admin", MetricSources: []MetricSource{{URL: server.URL + "/metrics", Token: "admin"}}, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	original, err := driver.qualificationBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.requested, state.authority = "disabled", "disabled"
	state.disclosureDigest = disabledDisclosureDigest
	state.ack = false
	state.mu.Unlock()
	if err = driver.restoreMode(original); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.disclosureDigest != readOnlyDisclosureDigest || !state.ack || len(state.acknowledgedDigests) != 1 || state.acknowledgedDigests[0] != readOnlyDisclosureDigest {
		t.Fatalf("restore did not re-acknowledge the exact restored digest: %#v", state)
	}
}

func TestRestoreModeRequiresFinalAcknowledgementAndReplicas(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*liveState)
	}{
		{name: "acknowledgement", mutate: func(state *liveState) {
			state.ack = false
			state.ignoreAcknowledgement = true
		}},
		{name: "replicas", mutate: func(state *liveState) {
			state.readyReplicas--
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := newLiveState()
			server := httptest.NewTLSServer(http.HandlerFunc(state.serveHTTP))
			defer server.Close()
			driver, err := NewLiveDriver(LiveConfig{AstronomerURL: server.URL, AdminToken: "admin", HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			original, err := driver.qualificationBaseline(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			state.mu.Lock()
			test.mutate(state)
			state.mu.Unlock()
			if err = driver.restoreMode(original); err == nil {
				t.Fatal("cleanup accepted a state that did not match the captured baseline")
			}
		})
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
	case r.Method == http.MethodGet && r.URL.Path == "/metrics":
		w.Header().Set("Content-Type", "text/plain")
		for _, key := range runtimeKeys {
			_, _ = fmt.Fprintf(w, "%s %d\n", defaultCounterMetrics()[key], s.runtime[key])
		}
		_, _ = fmt.Fprintf(w, "astronomer_charlie_downstream_boundary_calls_total{entrypoint=%q,operation=%q} %d\n", "tunnel_message", "other", s.downstream["tunnel"])
		_, _ = fmt.Fprintf(w, "astronomer_charlie_downstream_boundary_calls_total{entrypoint=%q,operation=%q} %d\n", "kubernetes_proxy", "other", s.downstream["proxy"])
		for _, operation := range []string{"kubernetes", "exec", "logs", "helm"} {
			_, _ = fmt.Fprintf(w, "astronomer_charlie_downstream_boundary_calls_total{entrypoint=%q,operation=%q} %d\n", "other", operation, s.downstream[operation])
		}
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
		wasEnabled := s.feature
		s.feature = body.Value
		if !body.Value {
			s.emergency = true
			s.requested, s.authority = "disabled", "disabled"
			s.disclosureDigest = disabledDisclosureDigest
			s.ack = false
			s.revision++
		} else if !wasEnabled {
			s.desiredReplicas, s.readyReplicas = 2, 2
			if s.incrementOnFeatureRestore {
				s.runtime["model_calls"]++
			}
		}
		_, _ = fmt.Fprintf(w, `{"data":{"value":%t,"is_default":false}}`, s.feature)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/charlie/status/":
		_, _ = fmt.Fprintf(w, `{"data":{"connection":{"connected":true,"disclosure_digest":%q,"disclosure_acknowledged":%t},"mode":{"requested":%q,"authoritative":%q,"revision":%d,"emergency_disabled":%t,"disclosure_digest":%q},"agent":{"desired_replicas":%d,"ready_replicas":%d,"agent_version":"v1.2.3","image_digest":"sha256:test"}}}`, s.disclosureDigest, s.ack, s.requested, s.authority, s.revision, s.emergency, s.disclosureDigest, s.desiredReplicas, s.readyReplicas)
	case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/charlie/mode/":
		var body struct {
			Mode                        string `json:"mode"`
			Revision                    *int64 `json:"revision"`
			Emergency                   bool   `json:"emergency_disable"`
			AcknowledgeDisclosureDigest string `json:"acknowledge_disclosure_digest"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		if body.AcknowledgeDisclosureDigest != "" {
			if body.Revision != nil || body.Mode != "" || body.Emergency || body.AcknowledgeDisclosureDigest != s.disclosureDigest {
				http.Error(w, "digest conflict", http.StatusConflict)
				return
			}
			if !s.ignoreAcknowledgement {
				s.ack = true
			}
			s.acknowledgedDigests = append(s.acknowledgedDigests, body.AcknowledgeDisclosureDigest)
			_, _ = fmt.Fprintf(w, `{"data":{"requested":%q,"authoritative":%q,"revision":%d,"emergency_disabled":%t}}`, s.requested, s.authority, s.revision, s.emergency)
			return
		}
		if body.Revision == nil || *body.Revision != s.revision {
			http.Error(w, "conflict", http.StatusConflict)
			return
		}
		if body.Emergency {
			s.emergency = true
			s.requested, s.authority = "disabled", "disabled"
			s.disclosureDigest = disabledDisclosureDigest
			s.ack = false
		} else if s.emergency {
			if body.Mode != "disabled" {
				http.Error(w, "must clear", http.StatusConflict)
				return
			}
			s.emergency = false
		} else {
			s.requested, s.authority = body.Mode, body.Mode
			if body.Mode == "read_only" {
				s.disclosureDigest = readOnlyDisclosureDigest
			} else {
				s.disclosureDigest = disabledDisclosureDigest
			}
			s.ack = false
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
		// Normal downstream-agent traffic is deliberately non-zero. Charlie
		// qualification must read the trusted-origin family below and ignore
		// this fleet-wide counter.
		`astronomer_downstream_boundary_calls_total{entrypoint="tunnel_message",operation="agent_command"} 999`,
		`astronomer_charlie_downstream_boundary_calls_total{entrypoint="tunnel_message",operation="other"} 1`,
		`astronomer_charlie_downstream_boundary_calls_total{entrypoint="kubernetes_proxy",operation="other"} 2`,
		`astronomer_charlie_downstream_boundary_calls_total{entrypoint="other",operation="kubernetes"} 3`,
		`astronomer_charlie_downstream_boundary_calls_total{entrypoint="other",operation="exec"} 4`,
		`astronomer_charlie_downstream_boundary_calls_total{entrypoint="other",operation="logs"} 5`,
		`astronomer_charlie_downstream_boundary_calls_total{entrypoint="other",operation="helm"} 6`)
	metrics := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprintln(w, strings.Join(lines, "\n")) }))
	defer metrics.Close()
	driver, err := NewLiveDriver(LiveConfig{AstronomerURL: metrics.URL, AdminToken: "admin", MetricSources: []MetricSource{{URL: metrics.URL}}, HTTPClient: metrics.Client()})
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

func TestCountersBindEachBearerToOnlyItsMetricEndpoint(t *testing.T) {
	var firstAuthorization, secondAuthorization string
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstAuthorization = r.Header.Get("Authorization")
		for _, key := range runtimeKeys {
			_, _ = fmt.Fprintf(w, "%s 0\n", defaultCounterMetrics()[key])
		}
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondAuthorization = r.Header.Get("Authorization")
		for _, operation := range []string{"kubernetes", "exec", "logs", "helm"} {
			_, _ = fmt.Fprintf(w, "astronomer_charlie_downstream_boundary_calls_total{entrypoint=%q,operation=%q} 0\n", "other", operation)
		}
		_, _ = fmt.Fprintln(w, `astronomer_charlie_downstream_boundary_calls_total{entrypoint="tunnel_message",operation="other"} 0`)
		_, _ = fmt.Fprintln(w, `astronomer_charlie_downstream_boundary_calls_total{entrypoint="kubernetes_proxy",operation="other"} 0`)
	}))
	defer second.Close()
	driver, err := NewLiveDriver(LiveConfig{
		AstronomerURL: first.URL, AdminToken: "admin", AllowHTTP: true,
		MetricSources: []MetricSource{{URL: first.URL, Token: "central-only"}, {URL: second.URL, Token: "product-only"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = driver.Counters(t.Context()); err != nil {
		t.Fatal(err)
	}
	if firstAuthorization != "Bearer central-only" || secondAuthorization != "Bearer product-only" {
		t.Fatalf("metric bearer crossed endpoints: first=%q second=%q", firstAuthorization, secondAuthorization)
	}
}

func TestLiveDriverRejectsDuplicateMetricEndpoint(t *testing.T) {
	_, err := NewLiveDriver(LiveConfig{
		AstronomerURL: "http://127.0.0.1:8000", AdminToken: "admin", AllowHTTP: true,
		MetricSources: []MetricSource{{URL: "http://127.0.0.1:9090/metrics"}, {URL: "http://127.0.0.1:9090/metrics", Token: "substitute"}},
	})
	if err == nil {
		t.Fatal("duplicate metric endpoint accepted with alternate credential")
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
