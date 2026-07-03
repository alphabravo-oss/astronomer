package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// AuditEventSender forwards a batch of raw audit.k8s.io Event JSON documents to
// the management plane's ingest endpoint
// (POST /api/v1/clusters/{cluster_id}/apiserver-audit/). It is an interface so
// the tail/batch/checkpoint core can be exercised without a live management
// plane — and, critically, because the agent has no usable outbound HTTP auth
// path to that endpoint today (see stubAuditSender). The default production
// wiring is a stub that drops batches and logs, until the auth gap is closed.
type AuditEventSender interface {
	// Send forwards a batch of raw JSON-encoded audit events. It must return a
	// non-nil error if the batch was not durably accepted, so the tailer does
	// NOT advance its checkpoint past un-delivered events.
	Send(ctx context.Context, events []json.RawMessage) error
}

// stubAuditSender is the default sender used until the agent gains an outbound
// auth path to the ingest endpoint. It logs the batch size and returns nil so
// the tailer advances its checkpoint (events are intentionally discarded — this
// is a clearly-stubbed sender, not a silent data path). Swap this for a real
// HTTP sender once the agent can authenticate to /apiserver-audit/.
type stubAuditSender struct {
	log *slog.Logger
}

func (s stubAuditSender) Send(_ context.Context, events []json.RawMessage) error {
	if s.log != nil {
		s.log.Info("apiserver-audit: stub sender dropping batch (no outbound auth path to ingest endpoint yet)",
			"events", len(events))
	}
	return nil
}

// AuditBatchSender is the ack-before-checkpoint contract tunnelAuditSender needs
// from the tunnel: send a MsgApiserverAudit frame tagged with batchID and BLOCK
// until the server acks it. It returns nil only when the server confirms durable
// persistence (ack OK=true); a persist failure, ack timeout, or disconnect
// returns an error so the tailer holds its checkpoint. Implemented by
// *TunnelClient.SendAuditBatch.
type AuditBatchSender interface {
	SendAuditBatch(ctx context.Context, batchID string, payload []byte) error
}

// tunnelAuditSender forwards audit batches over the existing agent WS tunnel as
// a protocol.MsgApiserverAudit frame, rather than over a separate outbound HTTP
// call. This is the default path (PATH B): it reuses the authenticated tunnel,
// so the server attributes events to the session's cluster ID and the agent
// needs no second credential.
//
// Delivery is AT-LEAST-ONCE: each batch gets a fresh BatchID and Send blocks on
// the server's matching MsgApiserverAuditAck, returning nil only on OK=true so
// the AuditTailer advances its checkpoint solely after durable persistence.
type tunnelAuditSender struct {
	sender AuditBatchSender
}

// newTunnelAuditSender wires a tunnelAuditSender to the agent's tunnel client.
func newTunnelAuditSender(sender AuditBatchSender) tunnelAuditSender {
	return tunnelAuditSender{sender: sender}
}

// Send marshals the batch (tagged with a fresh BatchID) into a MsgApiserverAudit
// frame and BLOCKS on the server's ack. An error — queue-full / closed tunnel,
// ack timeout, or a server persist failure — is returned so the tailer holds its
// checkpoint and re-forwards the batch (the ingest dedups on (cluster_id,
// audit_id), so re-delivery is safe).
func (s tunnelAuditSender) Send(ctx context.Context, events []json.RawMessage) error {
	if len(events) == 0 {
		return nil
	}
	batchID := uuid.NewString()
	body, err := json.Marshal(protocol.ApiserverAuditPayload{Events: events, BatchID: batchID})
	if err != nil {
		return fmt.Errorf("marshal apiserver-audit payload: %w", err)
	}
	return s.sender.SendAuditBatch(ctx, batchID, body)
}

// httpAuditSender forwards audit batches over a direct outbound HTTPS POST to
// the management plane's ingest endpoint, authenticating with a scoped Bearer
// token (PATH A). It is the alternative to tunnelAuditSender: rather than
// reusing the WS tunnel, it carries a narrowly-scoped API token
// (auth.AgentIngestTokenScopes == clusters:write only) issued to the agent and
// delivered out-of-band (CONNECT_ACK / K8s Secret). Used when the operator
// prefers HTTP delivery — e.g. so audit events take a different path than the
// proxy tunnel and don't share its backpressure.
//
// baseURL is the management-plane HTTP base (https://host[:port], no trailing
// slash); clusterID is fixed for the agent's lifetime. getToken is called on
// every Send so the ingest token delivered LATER in CONNECT_ACK (the agent
// dials, gets the ACK, then starts forwarding) is picked up lazily — and a
// rotated token (re-delivered on reconnect) takes effect on the next batch.
//
// fallback is the tunnel sender used while no token has arrived yet (or never
// does): the HTTP path can't authenticate without one, so rather than drop or
// stall audit events we deliver them over the authenticated tunnel and warn
// once. warned dedupes that warning.
type httpAuditSender struct {
	client    *http.Client
	baseURL   string
	clusterID string
	getToken  func() string
	fallback  AuditEventSender
	log       *slog.Logger
	warned    bool
}

// newHTTPAuditSender builds an httpAuditSender. wsServerURL is the agent's
// ServerURL (a ws:// / wss:// tunnel URL); it is rewritten to the matching
// http:// / https:// scheme since the ingest endpoint is a plain HTTP route on
// the same host. A nil client falls back to a default with a 30s timeout so a
// hung server can't wedge the tailer's poll loop forever. getToken is the lazy
// accessor for the CONNECT_ACK-delivered token; fallback carries batches over
// the tunnel until (or unless) a token arrives.
// newAuditHTTPClient builds the direct-HTTP audit client, trusting the same
// management CA bundle the tunnel pins (BuildTLSConfig). When no CA is
// configured BuildTLSConfig returns a nil tls.Config and we return a plain
// OS-trust client — identical to the pre-fix behavior for public-CA clusters.
func newAuditHTTPClient(cfg *AgentConfig) (*http.Client, error) {
	tlsCfg, err := BuildTLSConfig(cfg.CACert, cfg.CAChecksum)
	if err != nil {
		return nil, err
	}
	if tlsCfg == nil {
		return &http.Client{Timeout: 30 * time.Second}, nil
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}, nil
}

func newHTTPAuditSender(client *http.Client, wsServerURL, clusterID string, getToken func() string, fallback AuditEventSender, log *slog.Logger) *httpAuditSender {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &httpAuditSender{
		client:    client,
		baseURL:   httpBaseFromWS(wsServerURL),
		clusterID: clusterID,
		getToken:  getToken,
		fallback:  fallback,
		log:       log,
	}
}

// httpBaseFromWS rewrites a ws:// / wss:// tunnel URL to the http:// / https://
// base the ingest endpoint lives on, trimming any trailing slash. Non-ws
// schemes (already http/https) pass through with only the trailing slash
// trimmed, so a misconfigured-but-usable value still works.
func httpBaseFromWS(u string) string {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "wss://") {
		u = "https://" + strings.TrimPrefix(u, "wss://")
	} else if strings.HasPrefix(u, "ws://") {
		u = "http://" + strings.TrimPrefix(u, "ws://")
	}
	return strings.TrimSuffix(u, "/")
}

// Send POSTs the batch as {"events":[...]} to
// /api/v1/clusters/{cluster_id}/apiserver-audit/ with the scoped Bearer token.
// A non-2xx response (or a transport error) returns an error so the tailer
// holds its checkpoint and re-forwards the batch on the next poll. An empty
// batch is a no-op.
func (s *httpAuditSender) Send(ctx context.Context, events []json.RawMessage) error {
	if len(events) == 0 {
		return nil
	}
	// Lazily read the CONNECT_ACK-delivered token. Until it arrives, carry the
	// batch over the authenticated tunnel so audit events aren't dropped or
	// stalled while the handshake completes (or if the server never issues one).
	token := ""
	if s.getToken != nil {
		token = s.getToken()
	}
	if token == "" {
		if !s.warned && s.log != nil {
			s.log.Warn("apiserver-audit: AUDIT_DELIVERY=http but no ingest token yet; delivering over tunnel until one arrives")
			s.warned = true
		}
		if s.fallback != nil {
			return s.fallback.Send(ctx, events)
		}
		return fmt.Errorf("apiserver-audit: no ingest token and no tunnel fallback")
	}
	body, err := json.Marshal(protocol.ApiserverAuditPayload{Events: events})
	if err != nil {
		return fmt.Errorf("marshal apiserver-audit payload: %w", err)
	}
	url := fmt.Sprintf("%s/api/v1/clusters/%s/apiserver-audit/", s.baseURL, s.clusterID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build apiserver-audit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("post apiserver-audit batch: %w", err)
	}
	defer resp.Body.Close()
	// Drain so the connection can be reused (keep-alive).
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("apiserver-audit ingest returned status %d", resp.StatusCode)
	}
	return nil
}

// SelectAuditSender picks the audit sender from cfg.AuditDelivery. Keeping the
// choice in one helper means cmd/agent and tests pick the sender the same way.
//
//   - "tunnel" (default, and the empty/unknown fallback): reuse the
//     authenticated WS tunnel (PATH B) — no second credential needed, so audit
//     works out-of-the-box.
//   - "http": direct outbound HTTPS POST with the scoped ingest token (PATH A).
//     tokenGetter is read lazily on each batch so the token delivered in
//     CONNECT_ACK (which arrives AFTER the sender is selected) is picked up;
//     until/unless one arrives, batches fall back to the authenticated tunnel
//     (with a one-time warning), since the HTTP path can't authenticate
//     without a token.
//   - "stub": the no-op logging sender (batches+checkpoints but drops).
func SelectAuditSender(cfg *AgentConfig, tunnel AuditBatchSender, tokenGetter func() string, log *slog.Logger) AuditEventSender {
	switch cfg.AuditDelivery {
	case "stub":
		return stubAuditSender{log: log}
	case "http":
		// Build the outbound client with the same management CA bundle the tunnel
		// pins. Without this, on a private/self-signed-CA cluster every audit POST
		// fails TLS (OS trust store only), the tailer never advances its
		// checkpoint, and apiserver audit events are silently never delivered.
		client, err := newAuditHTTPClient(cfg)
		if err != nil {
			if log != nil {
				log.Warn("apiserver-audit: building HTTP client with CA bundle failed; falling back to tunnel delivery", "error", err)
			}
			return newTunnelAuditSender(tunnel)
		}
		return newHTTPAuditSender(client, cfg.ServerURL, cfg.ClusterID, tokenGetter, newTunnelAuditSender(tunnel), log)
	default: // "tunnel" and any unrecognized value
		return newTunnelAuditSender(tunnel)
	}
}

// AuditTailer tails a kube-apiserver JSON audit log file, batching new events
// and forwarding them via an AuditEventSender. A byte offset is persisted to a
// checkpoint file after each successfully-sent batch so events are not
// re-forwarded across agent restarts.
//
// The audit log is expected in kube-apiserver's --audit-log-format=json layout:
// one audit.k8s.io Event JSON object per line.
type AuditTailer struct {
	logPath        string
	checkpointPath string
	batchSize      int
	pollInterval   time.Duration
	sender         AuditEventSender
	log            *slog.Logger
}

// NewAuditTailer builds an AuditTailer from agent config. When sender is nil a
// stubAuditSender is used (see its doc for the auth-gap caveat). Returns an
// error if the config is incomplete.
func NewAuditTailer(cfg *AgentConfig, sender AuditEventSender, log *slog.Logger) (*AuditTailer, error) {
	if cfg.AuditLogPath == "" {
		return nil, fmt.Errorf("audit_log_path is required when audit forwarding is enabled")
	}
	checkpoint := cfg.AuditCheckpointPath
	if checkpoint == "" {
		checkpoint = cfg.AuditLogPath + ".checkpoint"
	}
	batch := cfg.AuditBatchSize
	if batch <= 0 {
		batch = 100
	}
	poll := time.Duration(cfg.AuditPollInterval) * time.Second
	if poll <= 0 {
		poll = 10 * time.Second
	}
	if sender == nil {
		sender = stubAuditSender{log: log}
	}
	return &AuditTailer{
		logPath:        cfg.AuditLogPath,
		checkpointPath: checkpoint,
		batchSize:      batch,
		pollInterval:   poll,
		sender:         sender,
		log:            log,
	}, nil
}

// Run polls the audit log on pollInterval, forwarding new events until ctx is
// cancelled. Errors on an individual poll are logged and retried on the next
// tick — a transient read or send failure does not stop the tailer.
func (t *AuditTailer) Run(ctx context.Context) {
	ticker := time.NewTicker(t.pollInterval)
	defer ticker.Stop()

	// Do an initial pass immediately rather than waiting a full interval.
	if err := t.poll(ctx); err != nil && ctx.Err() == nil {
		t.log.Warn("apiserver-audit: poll failed", "error", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := t.poll(ctx); err != nil && ctx.Err() == nil {
				t.log.Warn("apiserver-audit: poll failed", "error", err)
			}
		}
	}
}

// poll reads everything new since the persisted offset, forwards it in batches,
// and persists the new offset after each batch is accepted. It is exported via
// Run but kept separate so tests can drive a single deterministic pass.
func (t *AuditTailer) poll(ctx context.Context) error {
	offset, err := t.readCheckpoint()
	if err != nil {
		return fmt.Errorf("read checkpoint: %w", err)
	}

	f, err := os.Open(t.logPath)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()

	size, err := fileSize(f)
	if err != nil {
		return fmt.Errorf("stat audit log: %w", err)
	}
	// Log rotation / truncation: if the file shrank below our offset, the file
	// was rotated out from under us. Restart from the beginning so we don't
	// seek past EOF and silently skip the new file's contents.
	if offset > size {
		offset = 0
	}
	if offset == size {
		return nil // nothing new
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("seek audit log: %w", err)
	}

	return t.forward(ctx, f, offset)
}

// forward scans r line-by-line starting at startOffset (the byte position of
// the first byte read from r), batching parsed events and persisting the offset
// after each accepted batch. Partial trailing lines (a line the apiserver has
// not finished writing) are left for the next poll: the checkpoint only
// advances past complete, newline-terminated lines.
func (t *AuditTailer) forward(ctx context.Context, r io.Reader, startOffset int64) error {
	reader := bufio.NewReader(r)
	consumed := startOffset // bytes durably accounted for via the checkpoint
	pending := startOffset  // bytes read so far, including the current batch
	var batch []json.RawMessage

	flush := func() error {
		if len(batch) == 0 {
			consumed = pending
			return nil
		}
		if err := t.sender.Send(ctx, batch); err != nil {
			return fmt.Errorf("send batch: %w", err)
		}
		consumed = pending
		if err := t.writeCheckpoint(consumed); err != nil {
			return fmt.Errorf("write checkpoint: %w", err)
		}
		batch = batch[:0]
		return nil
	}

	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			// A trailing chunk without a newline is an incomplete line still
			// being written — do NOT count it toward the offset; reprocess it
			// next poll once it's newline-terminated.
			if err := flush(); err != nil {
				return err
			}
			return nil
		}
		if err != nil {
			// Flush what we have before surfacing the read error so a partial
			// poll still makes forward progress.
			if ferr := flush(); ferr != nil {
				return ferr
			}
			return fmt.Errorf("read audit log: %w", err)
		}
		pending += int64(len(line))

		ev, ok := parseAuditLine(line)
		if !ok {
			// Skip blank/malformed lines but still count their bytes so we
			// don't re-read them forever.
			continue
		}
		batch = append(batch, ev)
		if len(batch) >= t.batchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
}

// parseAuditLine validates that a single audit-log line is a JSON object and
// returns it as a RawMessage. Blank lines and non-object/garbage lines return
// ok=false so the caller skips them. We don't fully decode the event here — the
// ingest endpoint pulls the indexed fields out and stores the raw document.
func parseAuditLine(line []byte) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return nil, false
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, false
	}
	if trimmed[0] != '{' {
		// Audit events are JSON objects; arrays/scalars aren't events.
		return nil, false
	}
	return json.RawMessage(trimmed), true
}

// readCheckpoint returns the persisted byte offset, or 0 if no checkpoint
// exists yet (first run).
func (t *AuditTailer) readCheckpoint() (int64, error) {
	data, err := os.ReadFile(t.checkpointPath)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		// A corrupt checkpoint is treated as "start over" rather than wedging
		// the tailer; re-forwarding is idempotent at the ingest endpoint.
		return 0, nil
	}
	if n < 0 {
		return 0, nil
	}
	return n, nil
}

// writeCheckpoint atomically persists the byte offset by writing to a temp file
// and renaming, so a crash mid-write can't leave a truncated offset.
func (t *AuditTailer) writeCheckpoint(offset int64) error {
	tmp := t.checkpointPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(offset, 10)), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.checkpointPath)
}

func fileSize(f *os.File) (int64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
