// Package charliequalification implements an operator-started, loopback-only
// live qualification hook. It is deliberately not linked into the Astronomer
// server and exposes no production route.
package charliequalification

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	EffectsAcknowledgement = "I_UNDERSTAND_CHARLIE_LIVE_EFFECTS"
	maxRequestBytes        = 64 << 10
)

var (
	runIDPattern    = regexp.MustCompile(`^qualification-[a-z0-9-]{1,120}$`)
	scenarioPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

var requiredAssertions = map[string][]string{
	"feature_false": {"state_applied"}, "unactivated": {"state_applied"},
	"central_disabled": {"state_applied"}, "emergency_disabled": {"state_applied"},
	"read_denial":                {"authorization_denied", "product_calls_zero"},
	"approval_expiry":            {"approval_expired", "product_calls_zero"},
	"approval_once":              {"approval_consumed_once", "product_call_once"},
	"approval_replay":            {"approval_replay_denied", "additional_product_calls_zero"},
	"approval_reject":            {"approval_rejected", "product_calls_zero"},
	"auto_allowlisted_success":   {"allowlisted_action_succeeded", "product_call_once"},
	"auto_nonallowlisted_denial": {"nonallowlisted_action_denied", "product_calls_zero"},
	"discovery_mixed_catalog":    {"valid_capability_retained", "malformed_capability_rejected", "catalog_bound"},
	"malformed_discovery":        {"discovery_rejected", "integration_disabled", "product_calls_zero"},
	"versioned_rag_grounded":     {"real_generation", "real_embedding", "corrected_revision", "version_selected", "grounded_citation"},
	"general_answer":             {"real_generation", "general_answer", "no_fabricated_citation"},
	"diagnosis_alert":            {"one_alert", "valid_deep_link", "content_free"},
	"approval_pending_alert":     {"one_alert", "valid_deep_link", "content_free"},
	"approval_rejected_alert":    {"one_alert", "valid_deep_link", "content_free"},
	"approval_expired_alert":     {"one_alert", "valid_deep_link", "content_free"},
	"blocked_auto_alert":         {"one_alert", "valid_deep_link", "content_free"},
	"failed_precondition_alert":  {"one_alert", "valid_deep_link", "content_free"},
	"failed_verification_alert":  {"one_alert", "valid_deep_link", "content_free", "block_code_verification_failed"},
	"leader_kill_failover":       {"leader_identified", "leader_killed", "replacement_elected", "bounded_failover", "epoch_advanced", "sse_resumed", "no_duplicate_action"},
	"clean_install":              {"fresh_database", "migrations_applied", "pgvector_ready", "object_storage_ready", "oci_authenticated", "tls_enforced", "admin_login_succeeded", "development_bypass_absent", "candidate_digests_running"},
	"isolation_matrix":           {"two_deployments_same_product", "second_product_created", "credentials_isolated", "sessions_isolated", "usage_isolated", "findings_isolated", "audit_isolated", "cross_reads_denied"},
	"resilience_matrix":          {"restart_authority_not_increased", "leader_loss_recovered", "central_outage_failed_closed", "product_outage_failed_closed", "credential_rotation_converged", "disclosure_drift_disabled", "emergency_disable_drained", "recovery_required_explicit_enable"},
	"upgrade_rollback":           {"upgrade_candidate_pinned", "rollback_candidate_pinned", "authority_not_restored", "stale_credentials_rejected", "explicit_reenable_required"},
}

type Assertion struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

type ScenarioResult struct {
	Scenario   string      `json:"scenario"`
	Passed     bool        `json:"passed"`
	Assertions []Assertion `json:"assertions"`
}

type CounterSet struct {
	Runtime    map[string]uint64 `json:"runtime"`
	Downstream map[string]uint64 `json:"downstream"`
}

type Candidate struct {
	Ref                string `json:"ref"`
	Commit             string `json:"commit"`
	Version            string `json:"version"`
	CentralImageDigest string `json:"central_image_digest"`
	AgentImageDigest   string `json:"agent_image_digest"`
	CentralChartDigest string `json:"central_chart_digest"`
	AgentChartDigest   string `json:"agent_chart_digest"`
}

type ScenarioRequest struct {
	Schema    string    `json:"schema"`
	RunID     string    `json:"run_id"`
	Scenario  string    `json:"scenario"`
	Candidate Candidate `json:"candidate"`
}

type Driver interface {
	Counters(context.Context) (CounterSet, error)
	Run(context.Context, ScenarioRequest) ScenarioResult
}

type Hook struct {
	token  string
	driver Driver
	mu     sync.Mutex
}

func NewHook(token string, driver Driver) (*Hook, error) {
	token = strings.TrimSpace(token)
	if len(token) < 32 || len(token) > 512 || driver == nil {
		return nil, errors.New("qualification hook requires a strong bearer token and live driver")
	}
	return &Hook{token: token, driver: driver}, nil
}

func ValidateLoopbackAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return errors.New("qualification listen address must be an explicit loopback IP and port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("qualification hook must listen on loopback")
	}
	return nil
}

func (h *Hook) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/counters", h.authorize(h.counters))
	mux.HandleFunc("POST /v1/scenarios/{scenario}", h.authorize(h.scenario))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func (h *Hook) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(provided) != len(h.token) || subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (h *Hook) counters(w http.ResponseWriter, r *http.Request) {
	value, err := h.driver.Counters(r.Context())
	if err != nil || !completeCounters(value) {
		writeError(w, http.StatusServiceUnavailable, "counters_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *Hook) scenario(w http.ResponseWriter, r *http.Request) {
	scenario := r.PathValue("scenario")
	if !scenarioPattern.MatchString(scenario) || requiredAssertions[scenario] == nil {
		writeError(w, http.StatusNotFound, "scenario_unknown")
		return
	}
	limited := http.MaxBytesReader(w, r.Body, maxRequestBytes)
	defer func() { _ = limited.Close() }()
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var request ScenarioRequest
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		request.Schema != "charlie.live-scenario/v1" || request.Scenario != scenario || !runIDPattern.MatchString(request.RunID) {
		writeError(w, http.StatusBadRequest, "request_invalid")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	result := normalizeResult(h.driver.Run(r.Context(), request), scenario)
	writeJSON(w, http.StatusOK, result)
}

func normalizeResult(result ScenarioResult, scenario string) ScenarioResult {
	required := requiredAssertions[scenario]
	got := make(map[string]bool, len(result.Assertions))
	for _, item := range result.Assertions {
		got[item.Name] = item.Passed
	}
	assertions := make([]Assertion, 0, len(required))
	passed := result.Scenario == scenario && result.Passed
	for _, name := range required {
		value := got[name]
		assertions = append(assertions, Assertion{Name: name, Passed: value})
		passed = passed && value
	}
	return ScenarioResult{Scenario: scenario, Passed: passed, Assertions: assertions}
}

var runtimeKeys = []string{"model_calls", "rag_queries", "sessions", "mcp_calls", "tool_calls", "work_claims", "evidence_calls", "trigger_dispatches", "finding_dispatches"}
var downstreamKeys = []string{"tunnel", "proxy", "kubernetes", "exec", "logs", "helm"}

func completeCounters(value CounterSet) bool {
	for _, key := range runtimeKeys {
		if count, ok := value.Runtime[key]; !ok || count > 9_007_199_254_740_991 {
			return false
		}
	}
	for _, key := range downstreamKeys {
		if count, ok := value.Downstream[key]; !ok || count > 9_007_199_254_740_991 {
			return false
		}
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"code": code})
}

func NewHTTPServer(address string, handler http.Handler) (*http.Server, error) {
	if err := ValidateLoopbackAddress(address); err != nil {
		return nil, err
	}
	return &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 70 * time.Second, WriteTimeout: 21 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}, nil
}

func Unsupported(scenario string) ScenarioResult {
	return ScenarioResult{Scenario: scenario, Passed: false}
}

func Passed(scenario string, names ...string) ScenarioResult {
	assertions := make([]Assertion, 0, len(names))
	for _, name := range names {
		assertions = append(assertions, Assertion{Name: name, Passed: true})
	}
	return ScenarioResult{Scenario: scenario, Passed: true, Assertions: assertions}
}

func ValidateAcknowledgement(value string) error {
	if value != EffectsAcknowledgement {
		return fmt.Errorf("set effects acknowledgement to %s", EffectsAcknowledgement)
	}
	return nil
}
