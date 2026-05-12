// Package kubectl — input-side command recording for the WS stream.
//
// The WS upgrade itself reuses tunnel.ExecConsumer (sprint 14) — the
// handler wires the consumer to a synthetic exec target whose pod +
// container come from the session row. This file owns the
// command-extraction state machine that runs on every inbound stdin
// chunk so we can record an audit log entry per command without ever
// touching outbound stdout/stderr bytes.
package kubectl

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// MaxCommandLineLength caps a single recorded command line. Lines
// longer than this are truncated with "...<truncated>" before the
// INSERT so we don't store unbounded paste-bombs.
const MaxCommandLineLength = 1024

// CommandRecorder is the per-session state for input-side command
// extraction. Concurrency model: a single goroutine drives Feed() from
// the WS read loop, so we don't need locks on the buffer itself; the
// Flush() helper is callable from another goroutine on close and is
// guarded by mu.
type CommandRecorder struct {
	sessionID uuid.UUID
	queries   SessionQuerier
	log       *slog.Logger

	mu  sync.Mutex
	buf strings.Builder
}

// NewCommandRecorder builds a recorder pinned to one session row.
func NewCommandRecorder(sessionID uuid.UUID, queries SessionQuerier, log *slog.Logger) *CommandRecorder {
	if log == nil {
		log = slog.Default()
	}
	return &CommandRecorder{sessionID: sessionID, queries: queries, log: log}
}

// Feed consumes one inbound stdin chunk. The recorder treats \r or \n
// as line terminators (xterm sends \r on Enter by default) and emits
// one DB row per terminated line. Bytes after the last terminator are
// kept in the buffer for the next call.
//
// Output bytes from the agent NEVER go through this path — only stdin
// from the operator's browser. That's the compliance contract.
//
// Context is passed through to the DB INSERT; callers should pass the
// WS read-loop context so cancellation unwinds promptly.
func (r *CommandRecorder) Feed(ctx context.Context, chunk []byte) {
	if r == nil || len(chunk) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range chunk {
		if b == '\r' || b == '\n' {
			r.emit(ctx)
			continue
		}
		// Cap each pending line at 4×MaxCommandLineLength so a very
		// long paste before a newline can't blow up memory; we'll
		// still truncate to MaxCommandLineLength on emit.
		if r.buf.Len() < 4*MaxCommandLineLength {
			r.buf.WriteByte(b)
		}
	}
}

// Flush emits any buffered partial line (e.g. on disconnect mid-line).
// Idempotent.
func (r *CommandRecorder) Flush(ctx context.Context) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.emit(ctx)
}

// emit (caller holds r.mu) sends the current buffer to the DB if
// non-empty and resets the buffer. Whitespace-only lines are dropped
// so a stray Enter keypress doesn't write an audit row.
func (r *CommandRecorder) emit(ctx context.Context) {
	line := strings.TrimSpace(r.buf.String())
	r.buf.Reset()
	if line == "" {
		return
	}
	if len(line) > MaxCommandLineLength {
		line = line[:MaxCommandLineLength] + "...<truncated>"
	}
	if r.queries == nil {
		return
	}
	if err := r.queries.InsertKubectlSessionCommand(ctx, sqlc.InsertKubectlSessionCommandParams{
		SessionID:   r.sessionID,
		CommandLine: line,
	}); err != nil {
		r.log.Warn("kubectl: insert command failed",
			slog.String("session_id", r.sessionID.String()),
			slog.String("error", err.Error()))
	}
	// Stamp last_input_at so the idle reaper sees activity.
	if err := r.queries.TouchKubectlSessionInput(ctx, r.sessionID); err != nil {
		r.log.Debug("kubectl: touch last_input_at failed",
			slog.String("session_id", r.sessionID.String()),
			slog.String("error", err.Error()))
	}
}
