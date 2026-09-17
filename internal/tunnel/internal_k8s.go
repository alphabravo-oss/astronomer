// Internal cross-pod K8sRequest endpoint.
//
// The user-facing /api/v1/clusters/{id}/k8s/* proxy gained a redis-backed
// cross-pod fallback (proxy.go forwardToOwnerPod). But the same problem
// exists for server-internal tunnel calls — the shell session opener
// goes through handler.TunnelK8sRequester.Do, which calls Hub.SendToAgent
// directly. When the requester runs on a pod that doesn't own the
// agent's WS, it 503s.
//
// This file exposes a small POST endpoint that wraps the same
// SendToAgent + wait-for-response logic on whichever pod hosts it.
// Sibling pods call it (via TunnelK8sRequester's cross-pod fallback)
// instead of duplicating the HTTP-proxy code path.
//
// Auth: a short-lived HMAC envelope over the request path, body, cluster,
// nonce and originating user. Every server replica reads the same dedicated
// ASTRONOMER_INTERNAL_PSK; the Fernet key is never reused for this purpose.

package tunnel

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// InternalSourceHeader is a second, defense-in-depth signal that the
// request originated from a sibling server pod's cross-pod requester
// (k8s_requester.go / helm_requester.go) rather than from any external
// caller. Sibling requesters always set it to InternalSourceValue.
//
// This is NOT a secret — it can't be (it ships in the binary). Its sole
// purpose is to ensure that even if a misconfigured external ingress
// forwards /internal/* (the frontend.enabled=false catch-all, the dev
// nginx catch-all, etc.) AND an attacker has somehow learned the PSK,
// the request still fails closed unless it carries the in-band marker
// that an ingress proxy fronting the public listener does not add. It
// pairs with the explicit `/internal/` denies in the ingress/HTTPRoute/
// nginx templates: those keep external traffic off the path; this keeps
// the handler fail-closed if one of them ever drifts. Browser-origin
// requests routed through any of the shipped ingresses never set it.
const InternalSourceHeader = "X-Astronomer-Internal-Source"

// InternalSourceValue is the fixed marker sibling requesters place in
// InternalSourceHeader.
const InternalSourceValue = "sibling-server-pod"

// InternalForwardedUserHeader carries the UUID of the end user whose
// request the originating server pod is forwarding across the internal
// door. The originating pod knows the authenticated user (it ran the JWT
// /api-token middleware); it threads that identity here so the receiving
// pod — which mutates the target cluster on the user's behalf — can emit
// a user-attributed audit row matching the attribution the user-facing
// /api/v1/clusters/{id}/k8s/* proxy already records. Absent or unparsable
// values yield an anonymous (NULL user) row rather than dropping the
// audit entirely, so a mutation through the door is never unaudited.
const InternalForwardedUserHeader = "X-Astronomer-Forwarded-User"

// forwardedUserUUID parses the originating-user UUID off the internal
// request. uuid.Nil (and a NULL audit actor) is returned when the header
// is absent or malformed.
func forwardedUserUUID(r *http.Request) uuid.UUID {
	if r == nil {
		return uuid.Nil
	}
	got := strings.TrimSpace(r.Header.Get(InternalForwardedUserHeader))
	if got == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(got)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// hasSiblingSourceSignal reports whether the request carries the
// in-band sibling-pod source marker. Constant-time compared so the
// check doesn't leak the (non-secret but stable) value's length.
func hasSiblingSourceSignal(r *http.Request) bool {
	got := r.Header.Get(InternalSourceHeader)
	return subtle.ConstantTimeCompare([]byte(got), []byte(InternalSourceValue)) == 1
}

// InternalK8sHandler is the receiver-side of the cross-pod K8sRequest
// fallback. Mount it OUTSIDE the JWT auth middleware (it does its own
// PSK check) and BEFORE any rate limiter (server-internal traffic
// shouldn't share user quotas).
type InternalK8sHandler struct {
	hub  *Hub
	auth *internalRequestAuthenticator
	log  *slog.Logger
	// audit is the optional audit-log writer. When set, every mutating
	// request forwarded through this internal door emits a
	// cluster.k8s_proxy.forwarded row attributed to the originating user
	// (threaded via InternalForwardedUserHeader), matching the user-facing
	// proxy's attribution. nil leaves the door functional but unaudited —
	// used only by narrow tests; production wires it via SetAuditWriter.
	audit any
}

// InternalAgentCapabilityResponse is the authenticated sibling-RPC response
// used to make capability-aware reconciliation decisions on a server replica
// that does not own the target agent's WebSocket.
type InternalAgentCapabilityResponse struct {
	Supported bool `json:"supported"`
}

// SetAuditWriter wires the audit-log writer used to record a
// user-attributed cluster.k8s_proxy.forwarded row for every mutation that
// crosses this internal door. Pass the same *sqlc.Queries the user-facing
// proxy audits through. Safe to call with nil (audit disabled).
func (h *InternalK8sHandler) SetAuditWriter(w any) {
	if h == nil {
		return
	}
	h.audit = w
}

// NewInternalK8sHandler builds the handler. When psk is empty the
// handler 403s every request, which intentionally disables cross-pod
// internal RPC for deployments that haven't configured an encryption
// key. Such deployments are single-replica by design (the chart's
// production defaults wire the key) so the disable doesn't regress
// real users.
func NewInternalK8sHandler(hub *Hub, keys InternalRequestKeyring, log *slog.Logger) *InternalK8sHandler {
	if log == nil {
		log = slog.Default()
	}
	return &InternalK8sHandler{hub: hub, auth: newInternalRequestAuthenticator(keys, internalAudienceK8s), log: log}
}

// HandleCapability reports whether the locally-owned agent advertised one
// CONNECT-time capability. It never sends a message to the agent. The endpoint
// uses the same short-lived, path-bound sibling authentication as Handle so a
// non-owner replica can make the decision before attempting a mutation.
func (h *InternalK8sHandler) HandleCapability(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.hub == nil || !h.auth.enabled() {
		http.Error(w, `{"error":"internal endpoint disabled"}`, http.StatusServiceUnavailable)
		return
	}
	if !hasSiblingSourceSignal(r) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	clusterID := chi.URLParam(r, "cluster_id")
	capability := strings.TrimSpace(chi.URLParam(r, "capability"))
	if clusterID == "" || capability == "" {
		http.Error(w, `{"error":"cluster_id and capability are required"}`, http.StatusBadRequest)
		return
	}
	if !h.auth.verify(r, clusterID, nil) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	supported, connected := h.hub.SupportsAgentCapability(clusterID, capability)
	if !connected {
		http.Error(w, `{"error":"Cluster agent not connected"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(InternalAgentCapabilityResponse{Supported: supported})
}

// Handle is POST /internal/tunnel/k8s/{cluster_id}. Body is a
// JSON-encoded protocol.K8sRequestPayload. Response on success is a
// JSON-encoded protocol.K8sResponsePayload. Errors land as RFC-7807-ish
// JSON {"error": "..."} with the matching HTTP status.
func (h *InternalK8sHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if h == nil || !h.auth.enabled() {
		http.Error(w, `{"error":"internal endpoint disabled"}`, http.StatusServiceUnavailable)
		return
	}
	// Defense in depth: reject anything that didn't originate from a
	// sibling server pod's requester, even with a valid PSK. External
	// ingress paths never add this marker, so a /internal/* request that
	// leaked through a misconfigured catch-all fails closed here. Checked
	// before the PSK so a leaked-PSK attacker over an external route is
	// still denied.
	if !hasSiblingSourceSignal(r) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	clusterID := chi.URLParam(r, "cluster_id")
	if clusterID == "" {
		http.Error(w, `{"error":"cluster_id is required"}`, http.StatusBadRequest)
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
		return
	}
	if !h.auth.verify(r, clusterID, bodyBytes) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	var payload protocol.K8sRequestPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		http.Error(w, `{"error":"decode payload"}`, http.StatusBadRequest)
		return
	}
	// This door is TRANSPORT, not origin (design doc §10.3). A user-originated
	// request that landed on a non-owner replica arrives here still
	// user-originated, so whatever identity the sibling stamped is forwarded
	// VERBATIM — Fill only supplies one when the payload arrives unattributed,
	// which means the hop genuinely started at an internal caller. Note the
	// identity is taken from the PSK-authenticated sibling's payload, not from
	// any header on this request. PHASE 0: populated, unused.
	payload.CallerIdentity = callerid.Fill(
		callerid.WithMachine(r.Context(), callerid.SourceCrossPodTransport),
		payload.CallerIdentity,
	)

	agent := h.hub.GetAgent(clusterID)
	if agent == nil {
		// We were told we own this cluster (the caller looked us up via
		// the locator), but our WS dropped between the lookup and now.
		// Return 503 so the caller can either retry or surface the
		// disconnect to the end user.
		http.Error(w, `{"error":"Cluster agent not connected"}`, http.StatusServiceUnavailable)
		return
	}
	streamID := uuid.NewString()
	stream, sErr := agent.Streams.CreateStream(streamID)
	if sErr != nil {
		http.Error(w, `{"error":"`+sErr.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	defer agent.Streams.CloseStream(streamID)

	out, mErr := json.Marshal(payload)
	if mErr != nil {
		http.Error(w, `{"error":"`+mErr.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	if err := h.hub.SendToAgentContext(r.Context(), clusterID, &protocol.Message{
		Type:      protocol.MsgK8sRequest,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   out,
	}); err != nil {
		if errors.Is(err, ErrAgentCapabilityUnsupported) {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusPreconditionFailed)
			return
		}
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}

	waitCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	// The agent picks between a one-shot K8sResponsePayload and a
	// chunked K8sStreamFrame sequence (header + N data + end) based
	// on body size. Reading a single frame off DataCh — as this
	// handler used to — only ever captured the header on the
	// chunked path, returning a 200 with status_code + headers but
	// a zero-length body. That manifested on .247 as
	// "bravo cluster shows 0 pods" whenever the request crossed a
	// pod boundary. reassembleK8sResponse drives both shapes.
	resp, err := reassembleK8sResponse(waitCtx, stream.DataCh, stream.DoneCh)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, `{"error":"timeout"}`, http.StatusGatewayTimeout)
			return
		}
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	// Audit the mutation, attributed to the originating user the calling
	// pod threaded through the door. Mirrors the user-facing proxy's
	// cluster.k8s_proxy.forwarded row so a mutation that crossed a pod
	// boundary is never invisible in the audit trail. Best-effort: a
	// recording failure must not fail the (already-completed) cluster op.
	h.recordForwardedMutation(r, clusterID, &payload)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// internalK8sMutatingMethod reports whether the forwarded k8s method
// mutates cluster state. Mirrors the user-facing proxy's classification
// (only GET/HEAD/OPTIONS are non-mutating).
func internalK8sMutatingMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions, "":
		return false
	default:
		return true
	}
}

// recordForwardedMutation emits the user-attributed audit row for a
// mutation that crossed the internal door. No-op for reads or when no
// audit writer is configured.
func (h *InternalK8sHandler) recordForwardedMutation(r *http.Request, clusterID string, payload *protocol.K8sRequestPayload) {
	if h == nil || h.audit == nil || payload == nil {
		return
	}
	if !internalK8sMutatingMethod(payload.Method) {
		return
	}
	recordForwardedK8sMutationAudit(r, h.audit, forwardedUserUUID(r), clusterID, payload.Method, payload.Path)
}
