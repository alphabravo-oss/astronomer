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
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
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
	server            string
	clusters          int
	rps               int
	duration          time.Duration
	tokenPath         string
	outPath           string
	verbose           bool
	skipAgents        bool // dev convenience — disable WS dial entirely
	keepFixtures      bool // debug convenience — retain API-created cluster rows
	certification     bool
	validateDrills    bool
	profilePath       string
	profileName       string
	resources         scaleResources
	reconnectStorm    reconnectStormConfig
	day2FailureDrill  []string
	fixtureClusterIDs []string
	mandatoryAudit    mandatoryAuditProfile
	auditObserverPath string
}

func main() {
	cfg := parseFlags()
	if cfg.validateDrills {
		if err := validateConfiguredDrillEvidence(cfg, time.Now().UTC()); err != nil {
			fmt.Fprintf(os.Stderr, "validate drill evidence: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("validated %d drill evidence files for profile %s\n", len(cfg.day2FailureDrill), cfg.profileName)
		return
	}

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
	flag.StringVar(&cfg.auditObserverPath, "audit-observer-dsn", envOr("LOADTEST_AUDIT_OBSERVER_DATABASE_URL_FILE", ""), "path to a read-only PostgreSQL DSN used to independently observe durable audit outbox intents")
	flag.BoolVar(&cfg.verbose, "verbose", envOrBool("LOADTEST_VERBOSE", false), "log at debug level")
	flag.BoolVar(&cfg.skipAgents, "skip-agents", envOrBool("LOADTEST_SKIP_AGENTS", false), "do not dial synthetic agent WS — HTTP workload only")
	flag.BoolVar(&cfg.keepFixtures, "keep-fixtures", envOrBool("LOADTEST_KEEP_FIXTURES", false), "retain provisioned cluster fixtures for debugging")
	flag.BoolVar(&cfg.certification, "certification", envOrBool("LOADTEST_CERTIFICATION", false), "require reproducibility metadata and passing day-2 drill evidence")
	flag.BoolVar(&cfg.validateDrills, "validate-drill-evidence", false, "validate configured drill evidence provenance without running a load test")
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
	if cfg.certification && cfg.mandatoryAudit.RatePerSecond > 0 {
		if strings.TrimSpace(cfg.auditObserverPath) == "" {
			return fmt.Errorf("LOADTEST_AUDIT_OBSERVER_DATABASE_URL_FILE is required for certification")
		}
		info, err := os.Stat(cfg.auditObserverPath)
		if err != nil {
			return fmt.Errorf("audit observer DSN must be readable: %w", err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("audit observer DSN must be a regular file")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("audit observer DSN file permissions must not grant group or other access")
		}
	}

	// 1. Load token. Required unless -skip-agents AND rps==0.
	adminToken, err := loadToken(cfg.tokenPath)
	if err != nil {
		return fmt.Errorf("load token: %w", err)
	}

	// 2. Authenticate against /api/v1/auth/me/ — fail fast on bad creds /
	//    unreachable server. If the user explicitly didn't pass a token we
	//    still verify the server is reachable.
	if err := verifyServer(cfg.server, adminToken); err != nil {
		return fmt.Errorf("verify server reachability: %w", err)
	}
	log.Info("server reachable")

	// 3. Create a real cluster row and short-lived, cluster-bound registration
	// credential for every synthetic agent. The admin bearer remains reserved
	// for HTTP workload requests and is never presented to the agent tunnel.
	var agentCredentials []agentCredential
	var cleanupFixtures func()
	if !cfg.skipAgents {
		provisionCtx, provisionCancel := context.WithTimeout(context.Background(), 30*time.Minute)
		runID := strings.ReplaceAll(uuid.NewString()[:13], "-", "")
		agentCredentials, err = provisionSyntheticAgentCredentials(
			provisionCtx,
			&http.Client{Timeout: 15 * time.Second},
			cfg.server,
			adminToken,
			cfg.clusters,
			runID,
		)
		provisionCancel()
		if err != nil {
			return fmt.Errorf("provision synthetic agents: %w", err)
		}
		cfg.fixtureClusterIDs = make([]string, 0, len(agentCredentials))
		for _, credential := range agentCredentials {
			cfg.fixtureClusterIDs = append(cfg.fixtureClusterIDs, credential.ClusterID)
		}
		log.Info("synthetic agent fixtures provisioned", "count", len(agentCredentials), "run_id", runID)
		var cleanupOnce sync.Once
		cleanupFixtures = func() {
			cleanupOnce.Do(func() {
				if cfg.keepFixtures {
					log.Warn("retaining synthetic agent fixtures by request", "count", len(agentCredentials), "run_id", runID)
					return
				}
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cleanupCancel()
				cleanupProvisionedClusters(
					cleanupCtx,
					&http.Client{Timeout: 15 * time.Second},
					strings.TrimRight(cfg.server, "/"),
					adminToken,
					agentCredentials,
				)
				log.Info("synthetic agent fixture cleanup requested", "count", len(agentCredentials), "run_id", runID)
			})
		}
		defer cleanupFixtures()
	}

	ctx, cancel := context.WithCancel(context.Background())
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

	// 4. Spawn synthetic agents. Each agent dials the WS and behaves like a
	//    real agent. They keep running until ctx is cancelled.
	var agentWG sync.WaitGroup
	agents := make([]*syntheticAgent, 0, cfg.clusters)
	if !cfg.skipAgents {
		agents = make([]*syntheticAgent, cfg.clusters)
		sem := make(chan struct{}, registrationConcur)
		for i := 0; i < cfg.clusters; i++ {
			i := i
			agents[i] = newSyntheticAgent(cfg.server, agentCredentials[i], log, rec, cfg.resources)
			agentWG.Add(1)
			go func() {
				defer agentWG.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				agents[i].Run(ctx)
			}()
		}
		log.Info("spawning synthetic agents", "count", cfg.clusters)
		readyCtx, readyCancel := context.WithTimeout(ctx, 30*time.Minute)
		if err := waitForSyntheticAgents(readyCtx, agents); err != nil {
			readyCancel()
			return fmt.Errorf("wait for synthetic agents: %w", err)
		}
		readyCancel()
		log.Info("all synthetic agents connected", "count", cfg.clusters)
		if cfg.reconnectStorm.Enabled {
			go scheduleReconnectStorm(ctx, agents, cfg.reconnectStorm, cfg.duration, log)
		}
		if cfg.resources.EventsPerSecond > 0 {
			go emitSyntheticStateEvents(ctx, agents, cfg.resources.EventsPerSecond, rec, log)
		}
	}
	rec.MarkStart()

	// 5. Drive HTTP workload at the configured RPS.
	workloadCtx, workloadCancel := context.WithTimeout(ctx, cfg.duration)
	defer workloadCancel()

	var workloadWG sync.WaitGroup
	var auditMutationWG sync.WaitGroup
	if cfg.rps > 0 {
		workloadWG.Add(1)
		go func() {
			defer workloadWG.Done()
			driveWorkload(workloadCtx, ctx, cfg, adminToken, rec, log)
		}()
	}
	if cfg.mandatoryAudit.RatePerSecond > 0 {
		auditMutationWG.Add(1)
		go func() {
			defer auditMutationWG.Done()
			runMandatoryAuditWorkload(workloadCtx, cfg, adminToken, rec, log)
		}()
	}

	// 6. Scrape /metrics every 15s for the duration of the workload.
	workloadWG.Add(1)
	go func() {
		defer workloadWG.Done()
		scrapeMetricsLoop(workloadCtx, cfg.server, adminToken, rec, log)
	}()

	// Wait for the workload window. Then stop agents.
	workloadWG.Wait()
	auditMutationWG.Wait()
	rec.MarkEnd()
	log.Info("workload window complete, draining agents")
	if cfg.mandatoryAudit.RatePerSecond > 0 {
		drainCtx, drainCancel := context.WithTimeout(context.Background(), mandatoryAuditDrainTimeout)
		if err := observeMandatoryAuditIntents(drainCtx, cfg, rec); err != nil {
			log.Warn("mandatory-audit durable intent observation did not converge", "error", err)
		}
		if err := reconcileMandatoryAudit(drainCtx, cfg, adminToken, rec); err != nil {
			log.Warn("mandatory-audit conservation did not converge", "error", err)
		}
		drainCancel()
	}

	cancel()
	agentWG.Wait()

	// 6. Final metrics scrape outside the timeout — captures the steady-state
	//    after the workload stops.
	finalCtx, finalCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finalCancel()
	if err := scrapeOnce(finalCtx, cfg.server, adminToken, rec); err != nil {
		log.Warn("final scrape failed", "error", err)
	}

	// 8. Write the report.
	report := newReport(cfg, rec)
	if err := report.WriteFile(cfg.outPath); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	log.Info("report written", "path", cfg.outPath, "verdict", report.Verdict)

	// Echo the verdict line to stdout so callers piping the harness output
	// can grep it without opening the file.
	fmt.Println("VERDICT: " + report.Verdict)
	if report.Verdict != "pass" {
		if cleanupFixtures != nil {
			// os.Exit skips deferred functions, so explicitly clean the estate
			// after evidence is written and before returning the failing verdict.
			cleanupFixtures()
		}
		os.Exit(2)
	}
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
	bootstrap bool
	log       *slog.Logger
	rec       *recorder
	resources scaleResources

	mu        sync.Mutex
	writeMu   sync.Mutex
	ready     chan struct{}
	readyOnce sync.Once
	conn      *websocket.Conn
}

func newSyntheticAgent(server string, credential agentCredential, log *slog.Logger, rec *recorder, resources scaleResources) *syntheticAgent {
	return &syntheticAgent{
		server:    server,
		token:     credential.RegistrationToken,
		clusterID: credential.ClusterID,
		agentID:   "loadtest-" + credential.ClusterID[:8],
		bootstrap: true,
		log:       log.With("cluster_id", credential.ClusterID),
		rec:       rec,
		resources: resources,
		ready:     make(chan struct{}),
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

func (sa *syntheticAgent) sendSyntheticStateUpdate(ctx context.Context, sequence uint64) bool {
	payload, err := json.Marshal(map[string]any{
		"op": "modified", "kind": "Pod", "api_version": "v1",
		"namespace":        fmt.Sprintf("ns-%02d", sequence%100),
		"name":             fmt.Sprintf("event-pod-%08d", sequence),
		"resource_version": fmt.Sprintf("%d", sequence),
	})
	if err != nil {
		return false
	}
	sa.mu.Lock()
	conn := sa.conn
	sa.mu.Unlock()
	if conn == nil {
		return false
	}
	return sa.writeMessage(ctx, conn, &tunnelMessage{
		Type: "STATE_UPDATE", ClusterID: sa.clusterID,
		Timestamp: time.Now().UTC(), Payload: payload,
	}) == nil
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

// connectAndServe runs a single connection session.
func (sa *syntheticAgent) connectAndServe(ctx context.Context) error {
	dialCtx, dialCancel := context.WithTimeout(ctx, 15*time.Second)
	defer dialCancel()

	wsURL, err := tunnelURL(sa.server, sa.clusterID)
	if err != nil {
		return fmt.Errorf("derive ws url: %w", err)
	}

	sa.mu.Lock()
	connectToken := sa.token
	sa.mu.Unlock()
	hdr := http.Header{}
	if connectToken != "" {
		hdr.Set("Authorization", "Bearer "+connectToken)
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
		"token":         connectToken,
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
		Accepted   bool   `json:"accepted"`
		Reason     string `json:"reason"`
		AgentToken string `json:"agent_token"`
	}
	if err := json.Unmarshal(ack.Payload, &ackPayload); err != nil {
		return fmt.Errorf("decode CONNECT_ACK: %w", err)
	}
	if !ackPayload.Accepted {
		return fmt.Errorf("connection rejected: %s", ackPayload.Reason)
	}
	if err := sa.acceptAgentCredential(ackPayload.AgentToken); err != nil {
		return err
	}

	sa.rec.RecordConnect()
	sa.readyOnce.Do(func() { close(sa.ready) })
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
			if err := sa.writeMessage(ctx, conn, &pong); err != nil {
				return fmt.Errorf("send PONG: %w", err)
			}
		case "K8S_REQUEST":
			resp := sa.canned200Response(msg)
			if err := sa.writeMessage(ctx, conn, resp); err != nil {
				return fmt.Errorf("send K8S_RESPONSE: %w", err)
			}
		case "K8S_STREAM_REQUEST":
			// Reply with a header + empty data + end. Watch streams aren't
			// the load focus but a real agent would respond.
			for _, frame := range cannedStreamFrames(msg) {
				if err := sa.writeMessage(ctx, conn, frame); err != nil {
					return fmt.Errorf("send stream frame: %w", err)
				}
			}
		default:
			// Ignore unknown / not-load-relevant message types so the test
			// doesn't break when new types are added.
		}
	}
}

func (sa *syntheticAgent) acceptAgentCredential(agentToken string) error {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	if sa.bootstrap && (agentToken == "" || agentToken == sa.token) {
		return fmt.Errorf("accepted bootstrap connection did not provide a distinct durable agent credential")
	}
	if agentToken != "" {
		sa.token = agentToken
	}
	sa.bootstrap = false
	return nil
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
	if err := sa.writeMessage(ctx, conn, &msg); err != nil {
		sa.log.Debug("heartbeat send failed", "error", err)
	}
}

// writeMessage serializes websocket writes. The coder websocket contract
// permits one concurrent reader and one concurrent writer, not multiple
// writers (heartbeat, state-event, and K8S response goroutines).
func (sa *syntheticAgent) writeMessage(ctx context.Context, conn *websocket.Conn, msg *tunnelMessage) error {
	sa.writeMu.Lock()
	defer sa.writeMu.Unlock()
	return writeMsg(ctx, conn, msg)
}

// canned200Response builds a slim K8S_RESPONSE — the goal is to give the
// server something realistic in shape, not to model a full pod list.
func (sa *syntheticAgent) canned200Response(req *tunnelMessage) *tunnelMessage {
	body := sa.cannedK8sBody(req)
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
