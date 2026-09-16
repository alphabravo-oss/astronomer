package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Commands handles GET /clusters/{cluster_id}/shell/sessions/{id}/commands/.
func (h *KubectlShellHandler) Commands(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ShellUnavailable, "Kubectl shell is not configured")
		return
	}
	row, ok := h.loadSessionForCluster(w, r)
	if !ok {
		return
	}
	limit := queryLimit(r, 100)
	offset := queryOffset(r)
	if limit < 1 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := h.Queries.ListKubectlSessionCommands(r.Context(), sqlc.ListKubectlSessionCommandsParams{
		SessionID: row.ID,
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	total, _ := h.Queries.CountKubectlSessionCommands(r.Context(), row.ID)
	type wireCommand struct {
		CommandAt   time.Time `json:"command_at"`
		CommandLine string    `json:"command_line"`
	}
	out := make([]wireCommand, 0, len(rows))
	for _, c := range rows {
		out = append(out, wireCommand{CommandAt: c.CommandAt, CommandLine: c.CommandLine})
	}
	paging.Write(w, out, paging.Exact(total, queryLimit(r, 100), queryOffset(r), len(out)))
}

// AdminCommands handles GET /admin/shell-sessions/{id}/commands/.
// Superuser sees commands for ANY session, regardless of owner.
func (h *KubectlShellHandler) AdminCommands(w http.ResponseWriter, r *http.Request) {
	if !h.gateSuperuser(w, r) {
		return
	}
	idStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(idStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid session id")
		return
	}
	row, err := h.Queries.GetKubectlSessionByID(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Session not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	limit := queryLimitMax(r, 100, 1000)
	if limit < 1 {
		limit = 100
	}
	offset := queryOffset(r)
	rows, err := h.Queries.ListKubectlSessionCommands(r.Context(), sqlc.ListKubectlSessionCommandsParams{
		SessionID: row.ID, Limit: int32(limit), Offset: int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	total, _ := h.Queries.CountKubectlSessionCommands(r.Context(), row.ID)
	type wireCommand struct {
		CommandAt   time.Time `json:"command_at"`
		CommandLine string    `json:"command_line"`
	}
	out := make([]wireCommand, 0, len(rows))
	for _, c := range rows {
		out = append(out, wireCommand{CommandAt: c.CommandAt, CommandLine: c.CommandLine})
	}
	paging.Write(w, out, paging.Exact(total, queryLimitMax(r, 100, 1000), queryOffset(r), len(out)))
}

// drainRecordedCommands runs while the WS is open, inserting one
// kubectl_session_commands row per inbound line. The channel decouples
// the read loop (which must stay fast) from postgres latency.
func (h *KubectlShellHandler) drainRecordedCommands(ctx context.Context, sessionID uuid.UUID, ch <-chan string) {
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			if h.Queries == nil {
				continue
			}
			// Best-effort insert. A db error shouldn't kill the shell.
			if err := h.Queries.InsertKubectlSessionCommand(ctx, sqlc.InsertKubectlSessionCommandParams{
				SessionID:   sessionID,
				CommandLine: line,
			}); err != nil {
				h.logger().Warn("kubectl shell: failed to record command",
					slog.String("session_id", sessionID.String()),
					slog.String("error", err.Error()),
				)
			}
		}
	}
}

// indexLineTerminator returns the index of the first \r or \n in b, or
// -1 if none. Used by the input recorder to slice keystroke buffers
// into commands without dragging in bufio.Scanner (the data is
// arbitrary-length, not line-buffered, and we don't want bufio's
// MaxScanTokenSize gotcha on long pastes).
func indexLineTerminator(b []byte) int {
	for i, c := range b {
		if c == '\r' || c == '\n' {
			return i
		}
	}
	return -1
}

// sanitizeRecordedLine strips terminal-protocol noise from an
// otherwise-clean stdin line before it lands in the audit log.
//
// Two specific patterns matter for the kubectl-shell audit contract:
//
//  1. CSI escapes — `\x1b[ <params> <final>` (e.g. `\x1b[2;5R` cursor
//     position reports, `\x1b[A` arrow keys, `\x1b[1;5C` ctrl+right,
//     bracketed-paste markers like `\x1b[200~ … \x1b[201~`). These
//     are part of the xterm.js ↔ shell terminal protocol; the
//     operator never sees them as keystrokes.
//
//  2. C0 control characters other than tab — `\x00`-`\x1f` minus
//     `\t` and the line terminators (which are removed before
//     calling this fn). Backspace (`\x7f`) is also stripped because
//     the resulting recorded line is the line AFTER editing
//     (xterm.js handles the visible editing locally; only the final
//     pre-Enter buffer is what we care about).
//
// We deliberately do NOT process OSC (`\x1b]`) or SS2/SS3 (`\x1bN`,
// `\x1bO`) — they don't appear in stdin in any flow we ship today
// and adding them would broaden the surface without a known need.
func sanitizeRecordedLine(raw []byte) string {
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); {
		c := raw[i]
		// ESC (\x1b) starts a control sequence. Match CSI specifically:
		// ESC [ <param-bytes 0x30-0x3F>* <intermediate 0x20-0x2F>* <final 0x40-0x7E>.
		// Anything else after ESC we drop just the ESC and continue.
		if c == 0x1b {
			if i+1 < len(raw) && raw[i+1] == '[' {
				j := i + 2
				// Parameter + intermediate bytes.
				for j < len(raw) {
					b := raw[j]
					if (b >= 0x30 && b <= 0x3f) || (b >= 0x20 && b <= 0x2f) {
						j++
						continue
					}
					break
				}
				// Final byte (0x40-0x7e). Consume it if present.
				if j < len(raw) && raw[j] >= 0x40 && raw[j] <= 0x7e {
					i = j + 1
					continue
				}
				// Unterminated CSI — drop what we've seen and move on.
				i = j
				continue
			}
			// Lone ESC or non-CSI escape — drop the ESC only.
			i++
			continue
		}
		// Strip C0 controls (except tab) and DEL.
		if (c < 0x20 && c != '\t') || c == 0x7f {
			i++
			continue
		}
		out = append(out, c)
		i++
	}
	return strings.TrimRight(string(out), " \t")
}

// extractStdinBytes decodes one inbound WS frame into the raw stdin
// bytes the operator typed (or pasted).
//
// SECURITY (L3): this MUST mirror translateFromFrontend
// (internal/tunnel/exec_consumer.go) exactly — every frame that gets
// forwarded to the agent as stdin must also be recorded, or an attacker
// could evade the audit log by wrapping keystrokes in an unrecognized
// frame shape. translateFromFrontend forwards as stdin:
//
//   - {"type":"stdin"|"input","data":"…"}  → the data string
//   - any frame that is NOT a recognized control frame
//     (resize/auth/end/close) → the *raw frame bytes* (the fallback
//     branch in translateFromFrontend wraps the whole frame as stdin)
//
// So the only frames we ignore are the control frames, which never carry
// executed keystrokes. Everything else is recorded.
func extractStdinBytes(frame []byte) ([]byte, bool) {
	var env struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(frame, &env); err != nil {
		// Not valid JSON: translateFromFrontend forwards the raw bytes as
		// stdin, so we must record them too.
		return frame, true
	}
	switch env.Type {
	case "stdin", "input":
		return []byte(env.Data), true
	case "resize", "auth", "end", "close":
		// Control frames — never executed as keystrokes.
		return nil, false
	default:
		// Unrecognized type (or empty type): translateFromFrontend's
		// fallback forwards the whole frame to the agent as stdin, so it
		// IS executed. Record the raw frame so it can't go unaudited.
		return frame, true
	}
}
