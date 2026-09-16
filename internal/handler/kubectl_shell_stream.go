package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/coder/websocket"
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
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ShellUnavailable, "Kubectl shell is not configured")
		return
	}
	// WS upgrade requests come from browsers that can't set Authorization
	// headers, so the browser path is a short-lived ?ticket=.
	if h.JWT != nil {
		clusterID, _ := reqctx.ClusterID(r)
		userID, ok := auth.AuthorizeStreamRequestWithTickets(r, h.TokenQueries, h.JWT, h.StreamTickets, auth.StreamKindShell, clusterID)
		if !ok {
			RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
			return
		}
		// Inject the resolved user into the request context so
		// loadSessionForCluster resolves the same authenticated identity.
		r = r.WithContext(reqctx.WithUser(r.Context(), &reqctx.User{
			ID:         userID.String(),
			AuthMethod: "jwt",
		}))
	}
	row, ok := h.loadSessionForCluster(w, r)
	if !ok {
		return
	}
	if row.Status != "active" {
		RespondRequestError(w, r, http.StatusConflict, apierror.SessionNotActive, "Session is not active")
		return
	}
	// Stamp last_input_at — even just opening the WS counts as "engaged".
	_ = h.Queries.TouchKubectlSessionInput(r.Context(), row.ID)

	if h.Exec == nil {
		// Boot ordering bug — the server wiring forgot to call
		// SetExecProxy. Surface a clear 503 instead of silently 200ing
		// a WS that goes nowhere.
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ShellUnavailable, "Kubectl shell exec proxy is not configured")
		return
	}

	// Recheck current authorization before upgrading an existing session.
	wsScope, ok := h.deriveScopeForCaller(r.Context(), row.UserID, row.ClusterID, kubectl.EffectiveVerbs{Read: true, Update: true, Delete: true})
	if !ok {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Kubectl shell scope could not be determined for the caller")
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
			// Caller-scoping WS audit: DETECTIVE control layered on top of
			// the PREVENTIVE enforcement. As of DIR-04 mechanism A, a
			// confined session is provisioned with per-namespace Roles
			// (internal/kubectl), so an out-of-scope target is already
			// DENIED by the apiserver — the SA has no rights outside its
			// namespaces. This block still records the attempt for the
			// audit trail (onInput runs in the exec read loop and has no
			// back-channel to suppress the forward, but it no longer needs
			// one: the apiserver rejects it). The Open-time read-only verb
			// cap on namespace-scoped callers is defense-in-depth.
			if !wsScope.AllNamespaces {
				for _, target := range namespaceTargetsFromCommand(line) {
					if wsScope.Allows(target) {
						continue
					}
					shown := target
					if shown == namespaceAllSentinel {
						shown = "-A/--all-namespaces"
					}
					h.logger().Warn("kubectl shell: out-of-scope namespace target",
						slog.String("session_id", row.ID.String()),
						slog.String("user_id", row.UserID.String()),
						slog.String("cluster_id", row.ClusterID.String()),
						slog.String("target_namespace", shown),
					)
					recordAudit(r, h.Queries, "kubectl.session.out_of_scope", "cluster", row.ClusterID.String(), "", map[string]any{
						"session_id":       row.ID.String(),
						"target_namespace": shown,
						"allowed_scope":    wsScope.SortedNamespaces(),
					})
				}
			}
			// SECURITY (L3): recording must be reliable for an audited
			// break-glass feature. Rather than silently dropping the row
			// when the recorder is behind (the old default: case), we
			// BACK-PRESSURE: block this send until the drainer makes
			// room. Because onInput runs synchronously in the exec read
			// loop *before* the keystroke is forwarded to the agent
			// (see ExecConsumer.proxyToAgent), blocking here throttles
			// the session — the operator's command can't execute until
			// it has been queued for the audit log. recordCtx is
			// cancelled when the WS closes, so a dead drainer can't wedge
			// the read loop forever.
			select {
			case recordCh <- line:
			case <-recordCtx.Done():
				return
			}
		}
	}

	// §10.4 machine origin. The exec that bridges this WebSocket targets the
	// session POD, and the shell's per-caller enforcement is the session
	// ServiceAccount that kubectl.Open already provisioned from the caller's
	// CallerScope. Impersonation does not apply to it and must not be layered on
	// top, so the relay is stamped machine even though a human is on the other
	// end of the socket. The human remains attributed by the shell's own audit
	// trail (the session row plus the per-command recorder above).
	h.Exec.ProxyToAgentWithInputRecorder(
		callerid.WithMachine(r.Context(), callerid.SourceKubectlShell),
		conn, row.ClusterID.String(), row.PodNamespace, row.PodName, kubectl.ContainerName, onInput,
	)
}
