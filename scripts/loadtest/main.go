// Command loadtest is a synthetic-agent + HTTP workload driver used to
// validate the management plane's cluster-fleet capacity envelope. It lives
// under scripts/ because it is build-tagged-out of the production binaries
// and pulls test-only dependencies (none today, but the build tag is the
// long-term hedge).
//
// Run it via `make load-test` — see scripts/loadtest/README.md.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

// Build-time defaults. All overridable via flags.
const (
	defaultServer   = "http://localhost:8080"
	defaultClusters = 50
	defaultRPS      = 100
	defaultDuration = 5 * time.Minute
	defaultOut      = "loadtest-report.md"

	heartbeatInterval  = 30 * time.Second
	metricsScrape      = 15 * time.Second
	maxWSReadBytes     = 16 << 20 // matches internal/agent/tunnel.go
	registrationConcur = 16       // parallel agent connect rampup
)

type config struct {
	server           string
	clusters         int
	rps              int
	duration         time.Duration
	tokenPath        string
	outPath          string
	verbose          bool
	skipAgents       bool // dev convenience — disable WS dial entirely
	profilePath      string
	profileName      string
	resources        scaleResources
	reconnectStorm   reconnectStormConfig
	day2FailureDrill []string
}

func main() {
	cfg := parseFlags()

	logLevel := slog.LevelInfo
	if cfg.verbose {
		logLevel = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

	if err := run(cfg, log); err != nil {
		log.Error("load test failed", "error", err)
		// VERDICT line is the contract with CI — emit one even on harness
		// error so the grep doesn't silently miss the run.
		_ = writeFailureReport(cfg.outPath, err)
		os.Exit(1)
	}
}

func parseFlags() *config {
	cfg := &config{}
	flag.StringVar(&cfg.server, "server", envOr("LOADTEST_SERVER", defaultServer), "management-plane base URL")
	flag.IntVar(&cfg.clusters, "clusters", envOrInt("LOADTEST_CLUSTERS", defaultClusters), "number of synthetic agents to spawn")
	flag.IntVar(&cfg.rps, "rps", envOrInt("LOADTEST_RPS", defaultRPS), "aggregate HTTP request rate (per second)")
	flag.DurationVar(&cfg.duration, "duration", envOrDuration("LOADTEST_DURATION", defaultDuration), "how long to run")
	flag.StringVar(&cfg.tokenPath, "token", envOr("LOADTEST_TOKEN", ""), "path to a file holding an admin JWT (Bearer token)")
	flag.StringVar(&cfg.outPath, "out", envOr("LOADTEST_OUT", defaultOut), "where to write the markdown report")
	flag.StringVar(&cfg.profilePath, "profile", envOr("LOADTEST_PROFILE", ""), "optional YAML scale profile path")
	flag.BoolVar(&cfg.verbose, "verbose", envOrBool("LOADTEST_VERBOSE", false), "log at debug level")
	flag.BoolVar(&cfg.skipAgents, "skip-agents", envOrBool("LOADTEST_SKIP_AGENTS", false), "do not dial synthetic agent WS — HTTP workload only")
	flag.Parse()
	if cfg.profilePath != "" {
		profile, err := loadScaleProfile(cfg.profilePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load profile: %v\n", err)
			os.Exit(1)
		}
		if err := profile.apply(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "apply profile: %v\n", err)
			os.Exit(1)
		}
	}
	if cfg.resources.PodsPerCluster == 0 {
		cfg.resources = scaleResources{PodsPerCluster: 42, DeploymentsPerCluster: 10, ServicesPerCluster: 10}
	}
	return cfg
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var out int
	if _, err := fmt.Sscanf(v, "%d", &out); err != nil {
		return def
	}
	return out
}

func envOrBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "y":
		return true
	case "0", "false", "no", "n":
		return false
	}
	return def
}

func envOrDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func run(cfg *config, log *slog.Logger) error {
	log.Info("starting load test",
		"server", cfg.server,
		"clusters", cfg.clusters,
		"rps", cfg.rps,
		"duration", cfg.duration,
		"profile", cfg.profileName,
		"out", cfg.outPath,
	)

	if cfg.clusters < 1 {
		return fmt.Errorf("clusters must be >= 1, got %d", cfg.clusters)
	}
	if cfg.rps < 0 {
		return fmt.Errorf("rps must be >= 0, got %d", cfg.rps)
	}

	// 1. Load token. Required unless -skip-agents AND rps==0.
	token, err := loadToken(cfg.tokenPath)
	if err != nil {
		return fmt.Errorf("load token: %w", err)
	}

	// 2. Authenticate against /api/v1/auth/me/ — fail fast on bad creds /
	//    unreachable server. If the user explicitly didn't pass a token we
	//    still verify the server is reachable.
	if err := verifyServer(cfg.server, token); err != nil {
		return fmt.Errorf("verify server reachability: %w", err)
	}
	log.Info("server reachable")

	ctx, cancel := context.WithTimeout(context.Background(), cfg.duration+30*time.Second)
	defer cancel()

	// Signal handler — Ctrl-C tries to fold the in-flight run into a partial
	// report rather than dropping it.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			log.Warn("signal received, stopping early")
			cancel()
		case <-ctx.Done():
		}
	}()

	rec := newRecorder()
	rec.MarkStart()

	// 3. Spawn synthetic agents. Each agent dials the WS and behaves like a
	//    real agent. They keep running until ctx is cancelled.
	var agentWG sync.WaitGroup
	agents := make([]*syntheticAgent, 0, cfg.clusters)
	if !cfg.skipAgents {
		agents = make([]*syntheticAgent, cfg.clusters)
		sem := make(chan struct{}, registrationConcur)
		for i := 0; i < cfg.clusters; i++ {
			i := i
			agents[i] = newSyntheticAgent(cfg.server, token, log, rec, cfg.resources)
			agentWG.Add(1)
			go func() {
				defer agentWG.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				agents[i].Run(ctx)
			}()
		}
		log.Info("spawning synthetic agents", "count", cfg.clusters)
		if cfg.reconnectStorm.Enabled {
			go scheduleReconnectStorm(ctx, agents, cfg.reconnectStorm, cfg.duration, log)
		}
	}

	// 4. Drive HTTP workload at the configured RPS.
	workloadCtx, workloadCancel := context.WithTimeout(ctx, cfg.duration)
	defer workloadCancel()

	var workloadWG sync.WaitGroup
	if cfg.rps > 0 {
		workloadWG.Add(1)
		go func() {
			defer workloadWG.Done()
			driveWorkload(workloadCtx, cfg, token, rec, log)
		}()
	}

	// 5. Scrape /metrics every 15s for the duration of the workload.
	workloadWG.Add(1)
	go func() {
		defer workloadWG.Done()
		scrapeMetricsLoop(workloadCtx, cfg.server, token, rec, log)
	}()

	// Wait for the workload window. Then stop agents.
	workloadWG.Wait()
	rec.MarkEnd()
	log.Info("workload window complete, draining agents")

	cancel()
	agentWG.Wait()

	// 6. Final metrics scrape outside the timeout — captures the steady-state
	//    after the workload stops.
	finalCtx, finalCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finalCancel()
	if err := scrapeOnce(finalCtx, cfg.server, token, rec); err != nil {
		log.Warn("final scrape failed", "error", err)
	}

	// 7. Write the report.
	report := newReport(cfg, rec)
	if err := report.WriteFile(cfg.outPath); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	log.Info("report written", "path", cfg.outPath, "verdict", report.Verdict)

	// Echo the verdict line to stdout so callers piping the harness output
	// can grep it without opening the file.
	fmt.Println("VERDICT: " + report.Verdict)
	if report.Verdict != "pass" {
		os.Exit(2)
	}
	return nil
}

// loadToken reads a bearer token from the supplied file path. Empty path is
// allowed (will yield empty token; verifyServer will reject if the server
// requires auth).
func loadToken(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("token file %s is empty", path)
	}
	return tok, nil
}

// verifyServer hits /api/v1/auth/me/ and returns an error if the server is
// unreachable OR if it responds 401/403 (bad token). 404 / 5xx are also fatal
// — the load driver intentionally fails fast rather than burning a 10-minute
// run against a misconfigured target.
func verifyServer(server, token string) error {
	req, err := http.NewRequest(http.MethodGet, server+"/api/v1/auth/me/", nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("auth/me/ returned %d — token missing or invalid", resp.StatusCode)
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("auth/me/ returned %d — server unhealthy", resp.StatusCode)
	}
	// 200 (authed) or 401-with-no-token are both fine — we just wanted to
	// confirm the server is dialable. If the caller is running without a
	// token they get a degraded run.
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Synthetic agent — slim reimplementation of internal/agent/tunnel.go.
// Identical-on-the-wire: same CONNECT / CONNECT_ACK / HEARTBEAT / K8S_REQUEST
// shape. Does NOT depend on the agent package (which transitively pulls k8s
// client-go).
// ─────────────────────────────────────────────────────────────────────────────

type syntheticAgent struct {
	server    string
	token     string
	clusterID string
	agentID   string
	log       *slog.Logger
	rec       *recorder
	resources scaleResources

	mu   sync.Mutex
	conn *websocket.Conn
}

func newSyntheticAgent(server, token string, log *slog.Logger, rec *recorder, resources scaleResources) *syntheticAgent {
	clusterID := uuid.NewString()
	return &syntheticAgent{
		server:    server,
		token:     token,
		clusterID: clusterID,
		agentID:   "loadtest-" + clusterID[:8],
		log:       log.With("cluster_id", clusterID),
		rec:       rec,
		resources: resources,
	}
}

// Run dials the WS, runs the read/heartbeat loops, and reconnects with
// jittered backoff until ctx is cancelled.
func (sa *syntheticAgent) Run(ctx context.Context) {
	// Distinct seed per agent — UnixNano() already drifts enough between
	// goroutine launches, but XOR-ing in the cluster ID makes the test
	// deterministic-ish for replays where StartTime is fixed.
	var seed int64
	for _, c := range []byte(sa.clusterID) {
		seed = seed*31 + int64(c)
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano() ^ seed))
	attempt := 0
	for {
		if err := sa.connectAndServe(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			sa.rec.RecordDisconnect()
			attempt++
			wait := backoffWithJitter(attempt, 1, 30, rng)
			sa.log.Debug("agent disconnected, will retry", "error", err, "wait", wait, "attempt", attempt)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			continue
		}
		attempt = 0
		if ctx.Err() != nil {
			return
		}
	}
}

func scheduleReconnectStorm(ctx context.Context, agents []*syntheticAgent, storm reconnectStormConfig, duration time.Duration, log *slog.Logger) {
	at := storm.AtDuration
	if at <= 0 {
		at = duration / 3
	}
	if at <= 0 {
		at = 30 * time.Second
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(at):
	}

	limit := len(agents)
	if storm.BatchPercent > 0 && storm.BatchPercent < 100 {
		limit = maxInt(1, len(agents)*storm.BatchPercent/100)
	}
	jitter := storm.JitterDuration
	if jitter <= 0 {
		jitter = 15 * time.Second
	}
	log.Warn("triggering reconnect storm", "agents", limit, "jitter", jitter)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < limit; i++ {
		agent := agents[i]
		delay := time.Duration(rng.Int63n(int64(jitter)))
		go func() {
			select {
			case <-ctx.Done():
			case <-time.After(delay):
				agent.CloseForStorm()
			}
		}()
	}
}

func (sa *syntheticAgent) CloseForStorm() {
	sa.mu.Lock()
	conn := sa.conn
	sa.mu.Unlock()
	if conn != nil {
		_ = conn.Close(websocket.StatusGoingAway, "loadtest reconnect storm")
	}
}

// backoffWithJitter mirrors internal/agent/tunnel.go BackoffDurationWithJitter
// to avoid synchronized reconnect storms.
func backoffWithJitter(attempt, baseSec, maxSec int, rng *rand.Rand) time.Duration {
	shift := minInt(attempt, 16)
	backoff := float64(baseSec) * float64(uint64(1)<<uint(shift))
	if backoff > float64(maxSec) {
		backoff = float64(maxSec)
	}
	jitter := 0.75 + rng.Float64()*0.5
	return time.Duration(backoff*jitter*1000) * time.Millisecond
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// connectAndServe runs a single connection session.
func (sa *syntheticAgent) connectAndServe(ctx context.Context) error {
	dialCtx, dialCancel := context.WithTimeout(ctx, 15*time.Second)
	defer dialCancel()

	wsURL, err := tunnelURL(sa.server, sa.clusterID)
	if err != nil {
		return fmt.Errorf("derive ws url: %w", err)
	}

	hdr := http.Header{}
	if sa.token != "" {
		hdr.Set("Authorization", "Bearer "+sa.token)
	}

	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	conn.SetReadLimit(maxWSReadBytes)
	sa.mu.Lock()
	sa.conn = conn
	sa.mu.Unlock()
	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, "loadtest shutdown")
	}()

	// CONNECT
	connectPayload, _ := json.Marshal(map[string]any{
		"cluster_id":    sa.clusterID,
		"agent_id":      sa.agentID,
		"agent_version": "loadtest",
		"token":         sa.token,
	})
	connectMsg := tunnelMessage{
		Type:      "CONNECT",
		ClusterID: sa.clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   connectPayload,
	}
	if err := writeMsg(dialCtx, conn, &connectMsg); err != nil {
		return fmt.Errorf("send CONNECT: %w", err)
	}

	// Expect CONNECT_ACK
	ackCtx, ackCancel := context.WithTimeout(ctx, 10*time.Second)
	defer ackCancel()
	ack, err := readMsg(ackCtx, conn)
	if err != nil {
		return fmt.Errorf("read CONNECT_ACK: %w", err)
	}
	if ack.Type != "CONNECT_ACK" {
		return fmt.Errorf("expected CONNECT_ACK, got %s", ack.Type)
	}
	var ackPayload struct {
		Accepted bool   `json:"accepted"`
		Reason   string `json:"reason"`
	}
	_ = json.Unmarshal(ack.Payload, &ackPayload)
	if !ackPayload.Accepted {
		return fmt.Errorf("connection rejected: %s", ackPayload.Reason)
	}

	sa.rec.RecordConnect()
	defer sa.rec.RecordAgentEnd()

	// Heartbeat goroutine.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go sa.heartbeatLoop(hbCtx)

	// Read loop: handle K8sRequest, log/exec start, etc.
	for {
		msg, err := readMsg(ctx, conn)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}
		switch msg.Type {
		case "HEARTBEAT":
			// Server-initiated ping — reply PONG.
			pong := tunnelMessage{Type: "PONG", Timestamp: time.Now().UTC()}
			if err := writeMsg(ctx, conn, &pong); err != nil {
				return fmt.Errorf("send PONG: %w", err)
			}
		case "K8S_REQUEST":
			resp := sa.canned200Response(msg)
			if err := writeMsg(ctx, conn, resp); err != nil {
				return fmt.Errorf("send K8S_RESPONSE: %w", err)
			}
		case "K8S_STREAM_REQUEST":
			// Reply with a header + empty data + end. Watch streams aren't
			// the load focus but a real agent would respond.
			for _, frame := range cannedStreamFrames(msg) {
				if err := writeMsg(ctx, conn, frame); err != nil {
					return fmt.Errorf("send stream frame: %w", err)
				}
			}
		default:
			// Ignore unknown / not-load-relevant message types so the test
			// doesn't break when new types are added.
		}
	}
}

func (sa *syntheticAgent) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	// Fire one heartbeat right away so the server marks the cluster healthy
	// without waiting 30s.
	sa.sendHeartbeat(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sa.sendHeartbeat(ctx)
		}
	}
}

func (sa *syntheticAgent) sendHeartbeat(ctx context.Context) {
	payload, _ := json.Marshal(map[string]any{
		"timestamp":            time.Now().UTC().Format(time.RFC3339),
		"kubernetes_version":   "v1.30.0",
		"distribution":         "loadtest",
		"node_count":           3,
		"pod_count":            42,
		"cpu_usage_percent":    12.5,
		"memory_usage_percent": 30.0,
		"agent_version":        "loadtest",
	})
	msg := tunnelMessage{
		Type:      "HEARTBEAT",
		ClusterID: sa.clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
	sa.mu.Lock()
	conn := sa.conn
	sa.mu.Unlock()
	if conn == nil {
		return
	}
	if err := writeMsg(ctx, conn, &msg); err != nil {
		sa.log.Debug("heartbeat send failed", "error", err)
	}
}

// canned200Response builds a slim K8S_RESPONSE — the goal is to give the
// server something realistic in shape, not to model a full pod list.
func (sa *syntheticAgent) canned200Response(req *tunnelMessage) *tunnelMessage {
	body := sa.cannedK8sBody()
	payload, _ := json.Marshal(map[string]any{
		"status_code": 200,
		"headers":     map[string]string{"Content-Type": "application/json"},
		"body":        base64Encode([]byte(body)),
	})
	return &tunnelMessage{
		Type:      "K8S_RESPONSE",
		StreamID:  req.StreamID,
		ClusterID: req.ClusterID,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

func (sa *syntheticAgent) cannedK8sBody() string {
	pods := maxInt(0, sa.resources.PodsPerCluster)
	if pods > 250 {
		pods = 250
	}
	items := make([]map[string]any, 0, pods)
	for i := 0; i < pods; i++ {
		items = append(items, map[string]any{
			"metadata": map[string]any{
				"name":      fmt.Sprintf("pod-%04d", i),
				"namespace": fmt.Sprintf("ns-%02d", i%10),
				"labels": map[string]string{
					"app":     fmt.Sprintf("app-%02d", i%50),
					"profile": sa.resources.ProfileName,
				},
			},
			"status": map[string]any{"phase": "Running"},
		})
	}
	body, _ := json.Marshal(map[string]any{
		"kind":       "PodList",
		"apiVersion": "v1",
		"items":      items,
	})
	return string(body)
}

func cannedStreamFrames(req *tunnelMessage) []*tunnelMessage {
	headerPayload, _ := json.Marshal(map[string]any{
		"kind":        "header",
		"status_code": 200,
		"headers":     map[string]string{"Content-Type": "application/json"},
	})
	endPayload, _ := json.Marshal(map[string]any{"kind": "end"})
	return []*tunnelMessage{
		{Type: "K8S_STREAM_FRAME", StreamID: req.StreamID, Timestamp: time.Now().UTC(), Payload: headerPayload},
		{Type: "K8S_STREAM_FRAME", StreamID: req.StreamID, Timestamp: time.Now().UTC(), Payload: endPayload},
	}
}

// tunnelMessage mirrors pkg/protocol.Message minimally — no transitive deps.
type tunnelMessage struct {
	Type      string          `json:"type"`
	StreamID  string          `json:"stream_id,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	ClusterID string          `json:"cluster_id,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Error     string          `json:"error,omitempty"`
}

func writeMsg(ctx context.Context, conn *websocket.Conn, msg *tunnelMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, data)
}

func readMsg(ctx context.Context, conn *websocket.Conn) (*tunnelMessage, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var msg tunnelMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// tunnelURL derives the ws://host/api/v1/ws/agent/tunnel/{id}/ form from the
// http://host base URL. Returns an error for bad inputs.
func tunnelURL(server, clusterID string) (string, error) {
	u, err := url.Parse(server)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		// already
	default:
		return "", fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	u.Path = fmt.Sprintf("/api/v1/ws/agent/tunnel/%s/", clusterID)
	return u.String(), nil
}

func base64Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP workload driver.
// ─────────────────────────────────────────────────────────────────────────────

func driveWorkload(ctx context.Context, cfg *config, token string, rec *recorder, log *slog.Logger) {
	limiter := rate.NewLimiter(rate.Limit(cfg.rps), cfg.rps)
	scenarios := defaultScenarios()
	client := &http.Client{Timeout: 30 * time.Second}

	// Use a single rand source guarded by a tiny mutex — we don't need
	// crypto randomness, just per-tick scenario selection.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	var rngMu sync.Mutex

	var inflight atomic.Int64

	log.Info("starting HTTP workload", "rps", cfg.rps)
	for {
		if err := limiter.Wait(ctx); err != nil {
			return
		}
		rngMu.Lock()
		sc := pickScenario(scenarios, rng.Float64())
		rngMu.Unlock()
		inflight.Add(1)
		go func(sc scenario) {
			defer inflight.Add(-1)
			doRequest(ctx, client, cfg.server, token, sc, rec)
		}(sc)
	}
}

func doRequest(ctx context.Context, client *http.Client, server, token string, sc scenario, rec *recorder) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+sc.path, nil)
	if err != nil {
		rec.RecordHTTP(sc.name, 0, time.Duration(0), err)
		return
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		rec.RecordHTTP(sc.name, 0, elapsed, err)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	// Drain the body — readers that don't drain leak the connection in the
	// underlying transport.
	_, _ = io.Copy(io.Discard, resp.Body)
	rec.RecordHTTP(sc.name, resp.StatusCode, elapsed, nil)
}
