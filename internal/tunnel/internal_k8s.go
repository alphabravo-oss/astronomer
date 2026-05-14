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
// Auth: a PSK header. Both server pods read the same shared secret from
// the operator-provided ASTRONOMER_ENCRYPTION_KEY env var (which is
// already a shared secret across all pods via the platform's Helm
// secret). Requests without the matching PSK get 401 — outside callers
// can't reach this endpoint even if NetworkPolicy is misconfigured.

package tunnel

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// InternalPSKHeader is the request header the sibling pod fills with
// the shared-secret PSK. Pods derive the PSK from the encryption key so
// no extra plumbing is required to share it.
const InternalPSKHeader = "X-Astronomer-Internal-PSK"

// DerivePSK returns the PSK pods include in cross-pod internal requests.
// SHA-256 over the encryption-key bytes namespaced with a literal so
// the raw key is never sent on the wire and rotating the key rotates
// the PSK. Empty key returns "" — callers treat that as "internal
// endpoint disabled" and fall through to 503.
func DerivePSK(encryptionKey string) string {
	if encryptionKey == "" {
		return ""
	}
	h := sha256.Sum256([]byte("astronomer:internal:tunnel:k8s:v1:" + encryptionKey))
	return base64.StdEncoding.EncodeToString(h[:])
}

// InternalK8sHandler is the receiver-side of the cross-pod K8sRequest
// fallback. Mount it OUTSIDE the JWT auth middleware (it does its own
// PSK check) and BEFORE any rate limiter (server-internal traffic
// shouldn't share user quotas).
type InternalK8sHandler struct {
	hub *Hub
	psk string
	log *slog.Logger
}

// NewInternalK8sHandler builds the handler. When psk is empty the
// handler 403s every request, which intentionally disables cross-pod
// internal RPC for deployments that haven't configured an encryption
// key. Such deployments are single-replica by design (the chart's
// production defaults wire the key) so the disable doesn't regress
// real users.
func NewInternalK8sHandler(hub *Hub, psk string, log *slog.Logger) *InternalK8sHandler {
	if log == nil {
		log = slog.Default()
	}
	return &InternalK8sHandler{hub: hub, psk: psk, log: log}
}

// Handle is POST /internal/tunnel/k8s/{cluster_id}. Body is a
// JSON-encoded protocol.K8sRequestPayload. Response on success is a
// JSON-encoded protocol.K8sResponsePayload. Errors land as RFC-7807-ish
// JSON {"error": "..."} with the matching HTTP status.
func (h *InternalK8sHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.psk == "" {
		http.Error(w, `{"error":"internal endpoint disabled"}`, http.StatusServiceUnavailable)
		return
	}
	got := r.Header.Get(InternalPSKHeader)
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.psk)) != 1 {
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
	var payload protocol.K8sRequestPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		http.Error(w, `{"error":"decode payload"}`, http.StatusBadRequest)
		return
	}

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
	if err := h.hub.SendToAgent(clusterID, &protocol.Message{
		Type:      protocol.MsgK8sRequest,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   out,
	}); err != nil {
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
