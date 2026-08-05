package charliequalification

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type LiveConfig struct {
	AstronomerURL  string
	AdminToken     string
	DeniedToken    string
	MetricsURLs    []string
	MetricsToken   string
	CounterMetrics map[string]string
	AllowHTTP      bool
	HTTPClient     *http.Client
	AgentScaler    AgentScaler
}

type LiveDriver struct {
	base           *url.URL
	adminToken     string
	deniedToken    string
	metricsURLs    []*url.URL
	metricsToken   string
	counterMetrics map[string]string
	client         *http.Client
	agentScaler    AgentScaler
}

func NewLiveDriver(config LiveConfig) (*LiveDriver, error) {
	base, err := safeOperatorURL(config.AstronomerURL, config.AllowHTTP)
	if err != nil || strings.TrimSpace(config.AdminToken) == "" {
		return nil, errors.New("live driver requires a safe Astronomer URL and admin token")
	}
	metrics := make([]*url.URL, 0, len(config.MetricsURLs))
	for _, raw := range config.MetricsURLs {
		parsed, parseErr := safeOperatorURL(raw, config.AllowHTTP)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid metrics URL")
		}
		metrics = append(metrics, parsed)
	}
	mapping := defaultCounterMetrics()
	for key, name := range config.CounterMetrics {
		if !metricNamePattern.MatchString(name) || (!contains(runtimeKeys, key) && !contains(downstreamKeys, key)) {
			return nil, fmt.Errorf("invalid counter metric mapping")
		}
		mapping[key] = name
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout:       30 * time.Second,
			Transport:     &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return &LiveDriver{
		base: base, adminToken: strings.TrimSpace(config.AdminToken), deniedToken: strings.TrimSpace(config.DeniedToken),
		metricsURLs: metrics, metricsToken: strings.TrimSpace(config.MetricsToken), counterMetrics: mapping,
		client:      client,
		agentScaler: config.AgentScaler,
	}, nil
}

func safeOperatorURL(raw string, allowHTTP bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	httpLoopbackAllowed := allowHTTP && parsed.Scheme == "http" && netLoopback(parsed.Hostname())
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && !httpLoopbackAllowed) {
		return nil, errors.New("URL must use HTTPS without credentials, query, or fragment")
	}
	return parsed, nil
}

func netLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func defaultCounterMetrics() map[string]string {
	return map[string]string{
		"model_calls": "charlie_model_usage_total", "rag_queries": "charlie_rag_queries_total",
		"sessions": "charlie_sessions_created_total", "mcp_calls": "astronomer_charlie_mcp_calls_total",
		"tool_calls": "astronomer_charlie_actions_total", "work_claims": "charlie_agent_work_claims_total",
		"evidence_calls": "charlie_evidence_calls_total", "trigger_dispatches": "astronomer_charlie_trigger_events_total",
		"finding_dispatches": "charlie_findings_dispatched_total",
		"tunnel":             "astronomer_downstream_boundary_calls_total",
		"proxy":              "astronomer_downstream_boundary_calls_total",
		"kubernetes":         "astronomer_downstream_boundary_calls_total",
		"exec":               "astronomer_downstream_boundary_calls_total",
		"logs":               "astronomer_downstream_boundary_calls_total",
		"helm":               "astronomer_downstream_boundary_calls_total",
	}
}

func (d *LiveDriver) Counters(ctx context.Context) (CounterSet, error) {
	if len(d.metricsURLs) == 0 {
		return CounterSet{}, errors.New("metrics URLs are not configured")
	}
	samples := make([]metricSample, 0)
	for _, endpoint := range d.metricsURLs {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		request.Header.Set("Accept", "text/plain")
		if d.metricsToken != "" {
			request.Header.Set("Authorization", "Bearer "+d.metricsToken)
		}
		response, err := d.client.Do(request)
		if err != nil {
			return CounterSet{}, errors.New("metrics unavailable")
		}
		limited := &io.LimitedReader{R: response.Body, N: (16 << 20) + 1}
		parsed, parseErr := parsePrometheus(limited)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || parseErr != nil || limited.N == 0 {
			return CounterSet{}, errors.New("metrics invalid")
		}
		samples = append(samples, parsed...)
	}
	result := CounterSet{Runtime: map[string]uint64{}, Downstream: map[string]uint64{}}
	for _, key := range runtimeKeys {
		value, found := sumMetric(samples, d.counterMetrics[key], nil)
		if !found {
			return CounterSet{}, fmt.Errorf("required runtime counter %s is absent", key)
		}
		result.Runtime[key] = value
	}
	selectors := map[string]func(map[string]string) bool{
		"tunnel": func(v map[string]string) bool {
			return v["entrypoint"] == "tunnel_message" || v["entrypoint"] == "tunnel_broadcast" || v["entrypoint"] == "remote_dialer"
		},
		"proxy":      func(v map[string]string) bool { return v["entrypoint"] == "kubernetes_proxy" },
		"kubernetes": func(v map[string]string) bool { return v["operation"] == "kubernetes" },
		"exec":       func(v map[string]string) bool { return v["operation"] == "exec" },
		"logs":       func(v map[string]string) bool { return v["operation"] == "logs" },
		"helm":       func(v map[string]string) bool { return v["operation"] == "helm" },
	}
	for _, key := range downstreamKeys {
		metric := d.counterMetrics[key]
		value, found := sumMetric(samples, metric, selectors[key])
		if !found {
			// A newly started process may not have emitted a sample yet. The
			// metric family itself must still be present before zero is accepted.
			if !metricFamilyPresent(samples, metric) {
				return CounterSet{}, errors.New("downstream boundary metric is absent")
			}
		}
		result.Downstream[key] = value
	}
	return result, nil
}

func (d *LiveDriver) Run(ctx context.Context, request ScenarioRequest) ScenarioResult {
	switch request.Scenario {
	case "feature_false":
		return d.featureFalse(ctx, request.Scenario)
	case "unactivated":
		return d.unactivated(ctx, request)
	case "central_disabled":
		return d.centralDisabled(ctx, request.Scenario)
	case "emergency_disabled":
		return d.emergencyDisabled(ctx, request.Scenario)
	case "read_denial":
		return d.readDenial(ctx, request.Scenario)
	default:
		return Unsupported(request.Scenario)
	}
}

type settingEnvelope struct {
	Data struct {
		Value     json.RawMessage `json:"value"`
		IsDefault bool            `json:"is_default"`
	} `json:"data"`
}

func (d *LiveDriver) featureFalse(ctx context.Context, scenario string) (result ScenarioResult) {
	originalMode, err := d.qualificationBaseline(ctx)
	if err != nil {
		return Unsupported(scenario)
	}
	var original settingEnvelope
	if _, err = d.api(ctx, http.MethodGet, "/api/v1/admin/settings/feature.charlie/", d.adminToken, nil, &original); err != nil {
		return Unsupported(scenario)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		settingErr := d.restoreFeatureSetting(cleanupCtx, original)
		modeErr := d.restoreMode(originalMode)
		if settingErr != nil || modeErr != nil {
			result = Unsupported(scenario)
		}
	}()
	var applied settingEnvelope
	_, applyErr := d.api(ctx, http.MethodPut, "/api/v1/admin/settings/feature.charlie/", d.adminToken, map[string]any{"value": false}, &applied)
	if applyErr != nil || string(applied.Data.Value) != "false" {
		return Unsupported(scenario)
	}
	return Passed(scenario, "state_applied")
}

func (d *LiveDriver) restoreFeatureSetting(ctx context.Context, original settingEnvelope) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if original.Data.IsDefault {
			_, lastErr = d.api(ctx, http.MethodDelete, "/api/v1/admin/settings/feature.charlie/", d.adminToken, nil, nil)
		} else {
			_, lastErr = d.api(ctx, http.MethodPut, "/api/v1/admin/settings/feature.charlie/", d.adminToken, map[string]any{"value": original.Data.Value}, nil)
		}
		if lastErr == nil {
			var current settingEnvelope
			if _, lastErr = d.api(ctx, http.MethodGet, "/api/v1/admin/settings/feature.charlie/", d.adminToken, nil, &current); lastErr == nil &&
				current.Data.IsDefault == original.Data.IsDefault && string(current.Data.Value) == string(original.Data.Value) {
				return nil
			}
			if lastErr == nil {
				lastErr = errors.New("restored value did not match the original")
			}
		}
	}
	return fmt.Errorf("restore Charlie feature setting: %w", lastErr)
}

type statusEnvelope struct {
	Data struct {
		Connection struct {
			Connected              bool `json:"connected"`
			DisclosureAcknowledged bool `json:"disclosure_acknowledged"`
		} `json:"connection"`
		Mode struct {
			Requested         string `json:"requested"`
			Authoritative     string `json:"authoritative"`
			Revision          int64  `json:"revision"`
			EmergencyDisabled bool   `json:"emergency_disabled"`
		} `json:"mode"`
		Agent struct {
			DesiredReplicas int32  `json:"desired_replicas"`
			ReadyReplicas   int32  `json:"ready_replicas"`
			AgentVersion    string `json:"agent_version"`
			ImageDigest     string `json:"image_digest"`
		} `json:"agent"`
	} `json:"data"`
}

func (d *LiveDriver) status(ctx context.Context) (statusEnvelope, error) {
	var value statusEnvelope
	_, err := d.api(ctx, http.MethodGet, "/api/v1/admin/charlie/status/", d.adminToken, nil, &value)
	return value, err
}

func (d *LiveDriver) unactivated(ctx context.Context, request ScenarioRequest) (result ScenarioResult) {
	scenario := request.Scenario
	original, err := d.qualificationBaseline(ctx)
	if err != nil || d.agentScaler == nil {
		return Unsupported(scenario)
	}
	versionMatches := request.Candidate.Version != "" && original.Data.Agent.AgentVersion == request.Candidate.Version
	digestMatches := request.Candidate.AgentImageDigest != "" && original.Data.Agent.ImageDigest == request.Candidate.AgentImageDigest
	if !versionMatches && !digestMatches {
		return Unsupported(scenario)
	}
	replicas, err := d.agentScaler.Replicas(ctx)
	if err != nil || replicas < 1 {
		return Unsupported(scenario)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		scaleErr := d.agentScaler.Scale(cleanupCtx, replicas)
		var readyErr error
		if scaleErr == nil {
			readyErr = d.agentScaler.WaitReady(cleanupCtx, replicas)
		}
		modeErr := d.restoreMode(original)
		if scaleErr != nil || readyErr != nil || modeErr != nil {
			result = Unsupported(scenario)
		}
	}()
	if d.agentScaler.Scale(ctx, 0) != nil {
		return Unsupported(scenario)
	}
	if d.agentScaler.WaitReady(ctx, 0) != nil {
		return Unsupported(scenario)
	}
	return Passed(scenario, "state_applied")
}

func (d *LiveDriver) centralDisabled(ctx context.Context, scenario string) (result ScenarioResult) {
	original, err := d.qualificationBaseline(ctx)
	if err != nil {
		return Unsupported(scenario)
	}
	defer func() {
		if d.restoreMode(original) != nil {
			result = Unsupported(scenario)
		}
	}()
	if _, err = d.setMode(ctx, "disabled", original.Data.Mode.Revision, false); err != nil {
		return Unsupported(scenario)
	}
	applied, statusErr := d.status(ctx)
	if statusErr != nil || applied.Data.Mode.Requested != "disabled" || applied.Data.Mode.Authoritative != "disabled" {
		return Unsupported(scenario)
	}
	return Passed(scenario, "state_applied")
}

func (d *LiveDriver) emergencyDisabled(ctx context.Context, scenario string) (result ScenarioResult) {
	original, err := d.qualificationBaseline(ctx)
	if err != nil {
		return Unsupported(scenario)
	}
	defer func() {
		if d.restoreMode(original) != nil {
			result = Unsupported(scenario)
		}
	}()
	if _, err = d.setMode(ctx, "disabled", original.Data.Mode.Revision, true); err != nil {
		return Unsupported(scenario)
	}
	applied, err := d.status(ctx)
	if err != nil || !applied.Data.Mode.EmergencyDisabled || applied.Data.Mode.Authoritative != "disabled" {
		return Unsupported(scenario)
	}
	return Passed(scenario, "state_applied")
}

type modeEnvelope struct {
	Data struct {
		Requested         string `json:"requested"`
		Authoritative     string `json:"authoritative"`
		Revision          int64  `json:"revision"`
		EmergencyDisabled bool   `json:"emergency_disabled"`
	} `json:"data"`
}

func (d *LiveDriver) setMode(ctx context.Context, mode string, revision int64, emergency bool) (modeEnvelope, error) {
	var result modeEnvelope
	_, err := d.api(ctx, http.MethodPatch, "/api/v1/admin/charlie/mode/", d.adminToken, map[string]any{"mode": mode, "revision": revision, "emergency_disable": emergency}, &result)
	return result, err
}

func (d *LiveDriver) qualificationBaseline(ctx context.Context) (statusEnvelope, error) {
	status, err := d.status(ctx)
	if err != nil || !status.Data.Connection.Connected || status.Data.Mode.EmergencyDisabled || status.Data.Mode.Requested != "read_only" || status.Data.Mode.Authoritative != "read_only" || !status.Data.Connection.DisclosureAcknowledged {
		return statusEnvelope{}, errors.New("qualification requires an acknowledged read-only baseline")
	}
	return status, nil
}

func (d *LiveDriver) restoreMode(original statusEnvelope) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	current, err := d.status(ctx)
	if err != nil {
		return err
	}
	if current.Data.Mode.EmergencyDisabled {
		if _, err = d.setMode(ctx, "disabled", current.Data.Mode.Revision, false); err != nil {
			return err
		}
		current, err = d.status(ctx)
		if err != nil || current.Data.Mode.EmergencyDisabled {
			return errors.New("emergency latch cleanup failed")
		}
	}
	if current.Data.Mode.Requested != original.Data.Mode.Requested || current.Data.Mode.Authoritative != original.Data.Mode.Authoritative {
		if _, err = d.setMode(ctx, original.Data.Mode.Requested, current.Data.Mode.Revision, false); err != nil {
			return err
		}
	}
	final, err := d.status(ctx)
	if err != nil || final.Data.Mode.EmergencyDisabled || final.Data.Mode.Requested != original.Data.Mode.Requested || final.Data.Mode.Authoritative != original.Data.Mode.Authoritative || final.Data.Connection.Connected != original.Data.Connection.Connected || final.Data.Connection.DisclosureAcknowledged != original.Data.Connection.DisclosureAcknowledged {
		return errors.New("Charlie mode cleanup did not restore baseline")
	}
	return nil
}

func (d *LiveDriver) readDenial(ctx context.Context, scenario string) ScenarioResult {
	if d.deniedToken == "" {
		return Unsupported(scenario)
	}
	before, err := d.Counters(ctx)
	if err != nil {
		return Unsupported(scenario)
	}
	status, requestErr := d.api(ctx, http.MethodGet, "/api/v1/charlie/sessions/", d.deniedToken, nil, nil)
	after, counterErr := d.Counters(ctx)
	if counterErr != nil || status != http.StatusForbidden || requestErr == nil || after.Runtime["tool_calls"] != before.Runtime["tool_calls"] || !sameCounterMap(before.Downstream, after.Downstream) {
		return Unsupported(scenario)
	}
	return Passed(scenario, "authorization_denied", "product_calls_zero")
}

func sameCounterMap(before, after map[string]uint64) bool {
	for _, key := range downstreamKeys {
		if before[key] != after[key] {
			return false
		}
	}
	return true
}

func (d *LiveDriver) api(ctx context.Context, method, path, token string, body any, output any) (int, error) {
	endpoint := d.base.ResolveReference(&url.URL{Path: path})
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := d.client.Do(request)
	if err != nil {
		return 0, errors.New("Astronomer API unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	contents, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if readErr != nil || len(contents) > 1<<20 {
		return response.StatusCode, errors.New("Astronomer API response exceeded its bound")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, fmt.Errorf("Astronomer API returned HTTP %d", response.StatusCode)
	}
	if output != nil {
		decoder := json.NewDecoder(bytes.NewReader(contents))
		if err := decoder.Decode(output); err != nil {
			return response.StatusCode, errors.New("Astronomer API response invalid")
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return response.StatusCode, errors.New("Astronomer API response contains trailing data")
		}
	}
	return response.StatusCode, nil
}

type metricSample struct {
	name   string
	labels map[string]string
	value  uint64
}

var metricNamePattern = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
var samplePattern = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{([^}]*)\})?\s+([^\s]+)`)
var labelPattern = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)="((?:\\.|[^"])*)"`)

func parsePrometheus(reader io.Reader) ([]metricSample, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	result := []metricSample{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		match := samplePattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		value, err := strconv.ParseFloat(match[3], 64)
		if err != nil || value < 0 || value > 9_007_199_254_740_991 || math.Trunc(value) != value {
			continue
		}
		labels := map[string]string{}
		for _, item := range labelPattern.FindAllStringSubmatch(match[2], -1) {
			labels[item[1]] = item[2]
		}
		result = append(result, metricSample{name: match[1], labels: labels, value: uint64(value)})
	}
	return result, scanner.Err()
}

func sumMetric(samples []metricSample, name string, include func(map[string]string) bool) (uint64, bool) {
	var total uint64
	found := false
	for _, sample := range samples {
		if sample.name == name && (include == nil || include(sample.labels)) {
			if total > 9_007_199_254_740_991-sample.value {
				return 0, false
			}
			total += sample.value
			found = true
		}
	}
	return total, found
}
func metricFamilyPresent(samples []metricSample, name string) bool {
	for _, s := range samples {
		if s.name == name {
			return true
		}
	}
	return false
}
func contains[T comparable](values []T, wanted T) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
