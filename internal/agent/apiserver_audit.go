package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
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
