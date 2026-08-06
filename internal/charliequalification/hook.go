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

	charliecontract "github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
)

const (
	EffectsAcknowledgement = "I_UNDERSTAND_CHARLIE_LIVE_EFFECTS"
	maxRequestBytes        = 64 << 10
)

var (
	runIDPattern        = regexp.MustCompile(`^qualification-[a-z0-9-]{1,120}$`)
	scenarioPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	candidateRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	commitPattern       = regexp.MustCompile(`^[a-f0-9]{40}$`)
	versionPattern      = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
	digestPattern       = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

var requiredAssertions = func() map[string][]string {
	assertions, _ := charliecontract.QualificationScenarioContract()
	return assertions
}()

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
	token          string
	driver         Driver
	mu             sync.Mutex
	runID          string
	boundCandidate Candidate
	results        map[string]ScenarioResult
}

func NewHook(token string, driver Driver) (*Hook, error) {
	token = strings.TrimSpace(token)
	if len(token) < 32 || len(token) > 512 || driver == nil {
		return nil, errors.New("qualification hook requires a strong bearer token and live driver")
	}
	return &Hook{token: token, driver: driver, results: make(map[string]ScenarioResult)}, nil
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
		request.Schema != "charlie.live-scenario/v1" || request.Scenario != scenario || !runIDPattern.MatchString(request.RunID) ||
		!validCandidate(request.Candidate) {
		writeError(w, http.StatusBadRequest, "request_invalid")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runID == "" {
		h.runID, h.boundCandidate = request.RunID, request.Candidate
	} else if h.runID != request.RunID || h.boundCandidate != request.Candidate {
		writeError(w, http.StatusConflict, "qualification_binding_changed")
		return
	}
	if prior, ok := h.results[scenario]; ok {
		writeJSON(w, http.StatusOK, prior)
		return
	}
	result := normalizeResult(h.driver.Run(r.Context(), request), scenario)
	h.results[scenario] = result
	writeJSON(w, http.StatusOK, result)
}

func validCandidate(candidate Candidate) bool {
	return candidateRefPattern.MatchString(candidate.Ref) && commitPattern.MatchString(candidate.Commit) &&
		versionPattern.MatchString(candidate.Version) && digestPattern.MatchString(candidate.CentralImageDigest) &&
		digestPattern.MatchString(candidate.AgentImageDigest) && digestPattern.MatchString(candidate.CentralChartDigest) &&
		digestPattern.MatchString(candidate.AgentChartDigest)
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
