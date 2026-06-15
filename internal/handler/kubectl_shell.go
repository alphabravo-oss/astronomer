// Package handler — sprint 17 / migration 065 in-browser kubectl shell.
//
// REST surface (cluster-scoped):
//
//	POST   /api/v1/clusters/{cluster_id}/shell/sessions/
//	GET    /api/v1/clusters/{cluster_id}/shell/sessions/
//	GET    /api/v1/clusters/{cluster_id}/shell/sessions/{id}/
//	POST   /api/v1/clusters/{cluster_id}/shell/sessions/{id}/close/
//	GET    /api/v1/clusters/{cluster_id}/shell/sessions/{id}/commands/
//	GET    /api/v1/ws/clusters/{cluster_id}/shell/sessions/{id}/
//
// REST surface (admin):
//
//	GET    /api/v1/admin/shell-sessions/
//	GET    /api/v1/admin/shell-sessions/{id}/commands/
//
// All cluster-scoped routes are gated on clusters:update — opening a
// privileged shell isn't a read action. The WS endpoint additionally
// validates that the session row belongs to the caller (operators
// can't hijack each other's sessions). Admin routes are superuser-only
// inside the handler (mirrors admin_drill.go).
//
// Provisioning + RBAC mirroring lives in internal/kubectl; this file
// just owns the HTTP envelope, scope checks, and audit recording.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// ExecProxy is the slice of *tunnel.ExecConsumer this handler needs in
// order to bridge an already-upgraded WebSocket onto a cluster agent's
// exec stream. Defined as an interface so tests can substitute a fake
// without pulling the entire tunnel package into the handler test
// binary, and so the handler stays decoupled from tunnel internals.
type ExecProxy interface {
	ProxyToAgent(ctx context.Context, conn *websocket.Conn, clusterID, namespace, pod, container string)
	// ProxyToAgentWithInputRecorder is the audited variant used by the
	// kubectl-shell WS handler. onInput, if non-nil, receives each
	// inbound stdin/input frame's payload bytes before the relay
	// forwards them to the agent. The relay never blocks on the
	// callback; runaway recorders cannot stall the shell.
	ProxyToAgentWithInputRecorder(ctx context.Context, conn *websocket.Conn, clusterID, namespace, pod, container string, onInput func([]byte))
}

// KubectlShellQuerier is the slice of *sqlc.Queries the handler needs.
type KubectlShellQuerier interface {
	kubectl.SessionQuerier
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
}

// KubectlBindingsQuerier resolves the caller's RBAC bindings so the
// handler can map them to an EffectiveVerbs bundle without re-running
// the middleware (which already enforced clusters:update). Same shape
// as middleware.RBACQuerier.
type KubectlBindingsQuerier interface {
	GetUserBindings(ctx context.Context, userID string) ([]rbac.RoleBinding, error)
}

// KubectlShellHandler owns the REST + WS surface.
type KubectlShellHandler struct {
	Queries    KubectlShellQuerier
	Bindings   KubectlBindingsQuerier
	RBACEngine *rbac.Engine
	Deps       kubectl.Deps
	Log        *slog.Logger
	// JWT is the manager used by HandleWS to authenticate the WebSocket
	// upgrade request via ticket/query/header auth. Browsers cannot set
	// Authorization headers on WS handshakes, so the SPA passes the JWT
	// in the URL. Without this the route 401s — see the sibling
	// /api/v1/ws/exec/ + /api/v1/ws/logs/ routes which solve the same
	// problem the same way (via auth.AuthorizeStreamRequest).
	JWT *auth.JWTManager
	// TokenQueries lets AuthorizeStreamRequest verify api_token-style
	// bearer tokens (astro_*) against the DB. Nil-safe: JWT-only mode
	// works without it.
	TokenQueries  auth.TokenQuerier
	StreamTickets *auth.StreamTicketStore
	// Exec is the cluster-agent exec relay HandleWS bridges onto after
	// upgrading the inbound WebSocket. When nil the WS endpoint 503s —
	// we still register the route so the SPA gets a stable error rather
	// than a 404. Wired by the server boot path; tests can leave it nil
	// (the non-WS endpoints don't need it).
	Exec ExecProxy
	// crossPodWSForwarder lets HandleWS hand off the WS upgrade to a
	// sibling pod when the cluster's tunnel is held by a sibling
	// replica. Mirrors the HTTP-side forwardToOwnerPod fallback in
	// internal/tunnel/proxy.go. nil-safe: when unset the handler stays
	// on the local-only path (single-pod deployments).
	crossPodWSForwarder CrossPodWSForwarder
}

// CrossPodWSForwarder is the surface the shell handler uses to
// reverse-proxy an HTTP-Upgrade WebSocket request to whichever
// sibling pod owns the cluster's tunnel. Returns true when the
// request was forwarded (the response has been written); false when
// the caller should fall through to its existing local-handling
// path. internal/tunnel.ForwardWSToOwnerPod (wired via a closure in
// server wiring) is the production implementation.
type CrossPodWSForwarder func(w http.ResponseWriter, r *http.Request, clusterID string) bool

// SetCrossPodWSForwarder wires the WS reverse-proxy used by HandleWS
// when the cluster's tunnel is held by a sibling pod. Optional;
// single-pod deployments leave it nil.
func (h *KubectlShellHandler) SetCrossPodWSForwarder(fn CrossPodWSForwarder) {
	if h == nil {
		return
	}
	h.crossPodWSForwarder = fn
}

// SetStreamAuth wires the JWT manager + token querier used to
// authenticate WS upgrade requests via ?ticket= or Authorization header. Called from server
// wiring after NewKubectlShellHandler so the constructor signature
// doesn't have to grow.
func (h *KubectlShellHandler) SetStreamAuth(jwt *auth.JWTManager, q auth.TokenQuerier) {
	if h == nil {
		return
	}
	h.JWT = jwt
	h.TokenQueries = q
}

func (h *KubectlShellHandler) SetStreamTickets(tickets *auth.StreamTicketStore) {
	if h == nil {
		return
	}
	h.StreamTickets = tickets
}

// SetExecProxy wires the relay used by HandleWS to bridge the inbound
// WebSocket onto the cluster agent's exec stream. Mirrors SetStreamAuth
// — called from server wiring so the constructor signature stays
// stable across the v1→v2 (redirect→inline-proxy) migration.
func (h *KubectlShellHandler) SetExecProxy(p ExecProxy) {
	if h == nil {
		return
	}
	h.Exec = p
}

// NewKubectlShellHandler builds a wired handler. Any nil dep degrades
// the relevant endpoint to 503; the route is still registered so the
// frontend gets a stable 503 instead of a 404.
func NewKubectlShellHandler(queries KubectlShellQuerier, bindings KubectlBindingsQuerier, engine *rbac.Engine, deps kubectl.Deps) *KubectlShellHandler {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &KubectlShellHandler{
		Queries:    queries,
		Bindings:   bindings,
		RBACEngine: engine,
		Deps:       deps,
		Log:        deps.Log,
	}
}

// effectiveVerbsFor derives the EffectiveVerbs bundle for the operator
// against this cluster. Uses the rbac.Engine with bindings looked up
// from the bindings querier. Falls back to read-only on lookup failure
// (the middleware already proved clusters:update, so we know at least
// Update is true; the bindings call here is for the more permissive
// Delete bit).
func (h *KubectlShellHandler) effectiveVerbsFor(r *http.Request, userID string, clusterID uuid.UUID) kubectl.EffectiveVerbs {
	v := kubectl.EffectiveVerbs{Read: true, Update: true}
	if h.Bindings == nil || h.RBACEngine == nil {
		return v
	}
	bindings, err := h.Bindings.GetUserBindings(r.Context(), userID)
	if err != nil {
		return v
	}
	if h.RBACEngine.CheckSuperuser(bindings) {
		v.Superuser = true
		return v
	}
	if h.RBACEngine.CheckPermission(bindings, rbac.ResourceClusters, rbac.VerbDelete, clusterID, uuid.Nil) {
		v.Delete = true
	}
	return v
}

// Open handles POST /clusters/{cluster_id}/shell/sessions/.
func (h *KubectlShellHandler) Open(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil || h.Deps.Requester == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell is not configured")
		return
	}
	clusterID, ok := parseShellClusterID(r)
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_cluster_id", "Invalid cluster_id")
		return
	}
	userID, ok := shellCallerUUID(r)
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	if _, err := h.Queries.GetClusterByID(r.Context(), clusterID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "cluster_not_found", "Cluster not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	verbs := h.effectiveVerbsFor(r, userID.String(), clusterID)

	info, err := kubectl.Open(r.Context(), h.Deps, kubectl.OpenRequest{
		UserID:    userID,
		ClusterID: clusterID,
		Verbs:     verbs,
		ClientIP:  parseClientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, "shell_open_failed", err.Error())
		return
	}
	recordAudit(r, h.Queries, "kubectl.session.opened", "cluster", clusterID.String(), "", map[string]any{
		"session_id": info.ID.String(),
		"superuser":  verbs.Superuser,
		"verbs":      verbs.Verbs(),
	})
	RespondJSON(w, http.StatusCreated, info)
}

// List handles GET /clusters/{cluster_id}/shell/sessions/.
func (h *KubectlShellHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell is not configured")
		return
	}
	clusterID, ok := parseShellClusterID(r)
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_cluster_id", "Invalid cluster_id")
		return
	}
	rows, err := h.Queries.ListActiveKubectlSessionsByCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	out := make([]kubectl.SessionInfo, 0, len(rows))
	for _, row := range rows {
		count, _ := h.Queries.CountKubectlSessionCommands(r.Context(), row.ID)
		out = append(out, kubectl.ToSessionInfo(row, count, h.idleTimeout()))
	}
	RespondJSON(w, http.StatusOK, out)
}

// Get handles GET /clusters/{cluster_id}/shell/sessions/{id}/.
func (h *KubectlShellHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell is not configured")
		return
	}
	row, ok := h.loadSessionForCluster(w, r)
	if !ok {
		return
	}
	count, _ := h.Queries.CountKubectlSessionCommands(r.Context(), row.ID)
	RespondJSON(w, http.StatusOK, kubectl.ToSessionInfo(row, count, h.idleTimeout()))
}

// Close handles POST /clusters/{cluster_id}/shell/sessions/{id}/close/.
func (h *KubectlShellHandler) Close(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell is not configured")
		return
	}
	row, ok := h.loadSessionForCluster(w, r)
	if !ok {
		return
	}
	if err := kubectl.Close(r.Context(), h.Deps, row.ID); err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, "shell_close_failed", err.Error())
		return
	}
	recordAudit(r, h.Queries, "kubectl.session.closed", "cluster", row.ClusterID.String(), "", map[string]any{
		"session_id":       row.ID.String(),
		"duration_seconds": int64(time.Since(row.StartedAt).Seconds()),
	})
	RespondJSON(w, http.StatusOK, map[string]string{"status": "closed"})
}

// Commands handles GET /clusters/{cluster_id}/shell/sessions/{id}/commands/.
func (h *KubectlShellHandler) Commands(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell is not configured")
		return
	}
	row, ok := h.loadSessionForCluster(w, r)
	if !ok {
		return
	}
	limit := queryInt(r, "limit", 100)
	offset := queryInt(r, "offset", 0)
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
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
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
	RespondPaginated(w, r, out, total)
}

// AdminListAll handles GET /admin/shell-sessions/.
func (h *KubectlShellHandler) AdminListAll(w http.ResponseWriter, r *http.Request) {
	if !h.gateSuperuser(w, r) {
		return
	}
	rows, err := h.Queries.ListAllActiveKubectlSessions(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	out := make([]kubectl.SessionInfo, 0, len(rows))
	for _, row := range rows {
		count, _ := h.Queries.CountKubectlSessionCommands(r.Context(), row.ID)
		out = append(out, kubectl.ToSessionInfo(row, count, h.idleTimeout()))
	}
	RespondJSON(w, http.StatusOK, out)
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
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_session_id", "Invalid session id")
		return
	}
	row, err := h.Queries.GetKubectlSessionByID(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "session_not_found", "Session not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	rows, err := h.Queries.ListKubectlSessionCommands(r.Context(), sqlc.ListKubectlSessionCommandsParams{
		SessionID: row.ID, Limit: 1000, Offset: 0,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
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
	RespondPaginated(w, r, out, total)
}

// HandleWS handles GET /ws/clusters/{cluster_id}/shell/sessions/{id}/.
//
// Authenticates the upgrade request (ticket or token via query/header since
// browser WS handshakes can't carry custom Authorization headers),
// validates the session row belongs to this cluster + caller, then
// upgrades the WebSocket on the original route and proxies frames
// inline to the agent via ExecProxy.
//
// Prior to sprint 17+/v2 this issued a 307 redirect at the existing
// /api/v1/ws/exec/{cluster_id}/{ns}/{pod}/{container}/ relay. That
// worked in Chromium but redirects on WS handshakes are not portable —
// Firefox and a number of corporate proxies break. The in-handler
// proxy reuses ExecConsumer.ProxyToAgent so there is exactly one
// exec relay implementation; this path only adds the session lookup.
func (h *KubectlShellHandler) HandleWS(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell is not configured")
		return
	}
	// WS upgrade requests come from browsers that can't set Authorization
	// headers, so the browser path is a short-lived ?ticket=.
	if h.JWT != nil {
		clusterID, _ := uuid.Parse(chi.URLParam(r, "cluster_id"))
		userID, ok := auth.AuthorizeStreamRequestWithTickets(r, h.TokenQueries, h.JWT, h.StreamTickets, auth.StreamKindShell, clusterID)
		if !ok {
			RespondRequestError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication required")
			return
		}
		// Inject the resolved user into the request context so
		// loadSessionForCluster's existing GetAuthenticatedUser lookup
		// finds it (preserving the test fake's behavior).
		r = r.WithContext(middleware.SetAuthenticatedUserForTest(r.Context(), &middleware.AuthenticatedUser{
			ID:         userID.String(),
			AuthMethod: "jwt",
		}))
	}
	row, ok := h.loadSessionForCluster(w, r)
	if !ok {
		return
	}
	if row.Status != "active" {
		RespondRequestError(w, r, http.StatusConflict, "session_not_active", "Session is not active")
		return
	}
	// Stamp last_input_at — even just opening the WS counts as "engaged".
	_ = h.Queries.TouchKubectlSessionInput(r.Context(), row.ID)

	if h.Exec == nil {
		// Boot ordering bug — the server wiring forgot to call
		// SetExecProxy. Surface a clear 503 instead of silently 200ing
		// a WS that goes nowhere.
		RespondRequestError(w, r, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell exec proxy is not configured")
		return
	}

	// Cross-pod WS upgrade hand-off (multi-replica fix).
	//
	// Up until this point we've done session lookup + auth in the
	// receiving pod's context. Now: if the cluster's tunnel is owned
	// by a sibling pod (nginx pinned the WS to the wrong replica),
	// forward the entire HTTP-Upgrade dance to the sibling. The
	// sibling re-runs auth + session lookup and runs the exec relay
	// from the pod that actually holds the agent's WS — so K8S
	// stream frames never arrive on a pod that doesn't own the
	// stream. Mirrors the HTTP path's forwardToOwnerPod fallback
	// (internal/tunnel/proxy.go).
	//
	// We check *after* auth so a forged request can't reach the
	// sibling at all, and *before* websocket.Accept so the upgrade
	// itself lands on the right pod.
	if h.crossPodWSForwarder != nil {
		if forwarded := h.crossPodWSForwarder(w, r, row.ClusterID.String()); forwarded {
			return
		}
	}

	// Upgrade the inbound WS on the original route. We deliberately do
	// NOT 307-redirect to /api/v1/ws/exec/: Chromium followed the
	// redirect before the Upgrade handshake, Firefox does not, and
	// several corporate proxies strip Upgrade headers across redirects.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Browser handshakes can't choose origins for us; the route is
		// already gated by JWT/token auth above. Mirrors the
		// /api/v1/ws/exec/ + /ws/logs/ Accept call.
		InsecureSkipVerify: true,
	})
	if err != nil {
		h.logger().Error("kubectl shell websocket accept failed", slog.String("error", err.Error()))
		return
	}
	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, "closed")
	}()

	// Wire the input-line recorder: assemble inbound stdin bytes into
	// lines (terminated by \r or \n — the xterm TTY ships \r on Enter)
	// and flush each line as one kubectl_session_commands row. Output
	// bytes are never inspected; see docs/kubectl-shell.md §"Recording"
	// for the audit contract.
	recordCtx, cancelRecorder := context.WithCancel(context.Background())
	defer cancelRecorder()
	recordCh := make(chan string, 64)
	go h.drainRecordedCommands(recordCtx, row.ID, recordCh)

	var buf []byte
	onInput := func(frame []byte) {
		bytes, ok := extractStdinBytes(frame)
		if !ok || len(bytes) == 0 {
			return
		}
		buf = append(buf, bytes...)
		for {
			i := indexLineTerminator(buf)
			if i < 0 {
				break
			}
			// xterm.js mixes terminal-protocol replies into the same
			// stdin stream as keystrokes — when the server prompt
			// issues DSR (`\x1b[6n`, "report cursor position"), xterm
			// answers `\x1b[<row>;<col>R` and that response sails back
			// up the WS as if the user had typed it. Strip ANSI CSI
			// escapes (and a small set of other terminal-protocol
			// noise) before recording so the audit row reflects what
			// the operator actually typed.
			line := sanitizeRecordedLine(buf[:i])
			buf = buf[i+1:]
			if line == "" {
				continue
			}
			// Cap to 1KB per docs/kubectl-shell.md.
			if len(line) > 1024 {
				line = line[:1024] + "...<truncated>"
			}
			select {
			case recordCh <- line:
			default:
				// Recorder is behind. Dropping a row is preferable to
				// stalling the operator's shell.
			}
		}
	}

	h.Exec.ProxyToAgentWithInputRecorder(r.Context(), conn, row.ClusterID.String(), row.PodNamespace, row.PodName, kubectl.ContainerName, onInput)
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
// bytes the operator typed (or pasted). The browser sends a JSON
// envelope `{"type":"stdin"|"input","data":"…"}`; resize/auth frames
// and anything malformed return ok=false so the recorder ignores them.
func extractStdinBytes(frame []byte) ([]byte, bool) {
	var env struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(frame, &env); err != nil {
		return nil, false
	}
	if env.Type != "stdin" && env.Type != "input" {
		return nil, false
	}
	return []byte(env.Data), true
}

// logger returns the handler's logger, falling back to slog.Default()
// when the handler was constructed without one. HandleWS reaches for
// this on the WS-accept failure path; the rest of the file uses h.Log
// directly because those paths are guarded by NewKubectlShellHandler's
// default.
func (h *KubectlShellHandler) logger() *slog.Logger {
	if h != nil && h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

// loadSessionForCluster pulls the {cluster_id}/{id} pair from chi and
// returns the row only when it exists, belongs to this cluster, AND
// (for non-superuser callers) belongs to the caller. Writes the
// appropriate error response on failure.
func (h *KubectlShellHandler) loadSessionForCluster(w http.ResponseWriter, r *http.Request) (sqlc.KubectlSession, bool) {
	clusterID, ok := parseShellClusterID(r)
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_cluster_id", "Invalid cluster_id")
		return sqlc.KubectlSession{}, false
	}
	idStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(idStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_session_id", "Invalid session id")
		return sqlc.KubectlSession{}, false
	}
	row, err := h.Queries.GetKubectlSessionByID(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "session_not_found", "Session not found")
			return sqlc.KubectlSession{}, false
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return sqlc.KubectlSession{}, false
	}
	if row.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, "session_not_found", "Session not found in this cluster")
		return sqlc.KubectlSession{}, false
	}
	callerID, ok := shellCallerUUID(r)
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return sqlc.KubectlSession{}, false
	}
	if row.UserID != callerID && !h.callerIsSuperuser(r, callerID) {
		RespondRequestError(w, r, http.StatusForbidden, "session_not_owned", "Session belongs to another operator")
		return sqlc.KubectlSession{}, false
	}
	return row, true
}

func (h *KubectlShellHandler) callerIsSuperuser(r *http.Request, callerID uuid.UUID) bool {
	if h.Queries == nil {
		return false
	}
	u, err := h.Queries.GetUserByID(r.Context(), callerID)
	if err != nil {
		return false
	}
	return u.IsSuperuser
}

func (h *KubectlShellHandler) gateSuperuser(w http.ResponseWriter, r *http.Request) bool {
	callerID, ok := shellCallerUUID(r)
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return false
	}
	if h.Queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell is not configured")
		return false
	}
	user, err := h.Queries.GetUserByID(r.Context(), callerID)
	if err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", "Caller not found")
		return false
	}
	if !user.IsSuperuser {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", "Kubectl shell admin views require superuser privileges")
		return false
	}
	return true
}

func (h *KubectlShellHandler) idleTimeout() time.Duration {
	if h.Deps.IdleTimeout > 0 {
		return h.Deps.IdleTimeout
	}
	return 30 * time.Minute
}

// parseShellClusterID extracts and parses the {cluster_id} URL param.
func parseShellClusterID(r *http.Request) (uuid.UUID, bool) {
	idStr := chi.URLParam(r, "cluster_id")
	if idStr == "" {
		return uuid.UUID{}, false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.UUID{}, false
	}
	return id, true
}

// shellCallerUUID resolves the authenticated user's UUID from the request
// context. Returns (uuid.Nil, false) on any failure.
func shellCallerUUID(r *http.Request) (uuid.UUID, bool) {
	u, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || u == nil {
		return uuid.UUID{}, false
	}
	parsed, err := uuid.Parse(u.ID)
	if err != nil {
		return uuid.UUID{}, false
	}
	return parsed, true
}

// KubectlK8sRequesterAdapter wraps a handler-side K8sRequester in the
// shape kubectl.K8sRequester expects. Used by NewApp wiring so the
// in-cluster shell objects flow through the same tunnel circuit-
// breaker as every other tunnel mutation.
type kubectlRequesterAdapter struct{ r K8sRequester }

// Do implements kubectl.K8sRequester.
func (a kubectlRequesterAdapter) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*kubectl.K8sResponse, error) {
	resp, err := a.r.Do(ctx, clusterID, method, path, body, headers)
	if err != nil {
		return nil, err
	}
	b, _ := decodeResponseBody(resp)
	return &kubectl.K8sResponse{StatusCode: resp.StatusCode, Body: b}, nil
}

// KubectlK8sRequesterFromHandlerRequester adapts a handler.K8sRequester
// into the surface kubectl.Open / Close / Reap take. Mirrors
// ProjectK8sRequesterFromHandlerRequester. Returns nil when given nil
// so server.NewApp can pass through a missing tunnel hub cleanly.
func KubectlK8sRequesterFromHandlerRequester(r K8sRequester) kubectl.K8sRequester {
	if r == nil {
		return nil
	}
	return kubectlRequesterAdapter{r: r}
}

// parseClientIP best-effort extracts the client IP. RemoteAddr is the
// closest hop; if a real-IP middleware ran upstream it will have
// overwritten this with the original client. Splits the trailing
// :port off before parsing. Returns nil on parse failure (the column
// is nullable).
func parseClientIP(r *http.Request) *netip.Addr {
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		// Handle [::1]:1234
		if strings.HasPrefix(addr, "[") {
			if j := strings.Index(addr, "]"); j > 0 {
				addr = addr[1:j]
			}
		} else {
			addr = addr[:i]
		}
	}
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return nil
	}
	return &a
}
