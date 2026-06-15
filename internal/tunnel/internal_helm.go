// Internal cross-pod Helm request endpoint.
//
// Counterpart to internal_k8s.go for HELM_INSTALL / UPGRADE / UNINSTALL /
// STATUS / ROLLBACK round-trips. Hub.SendHelmRequest fronts the agent over
// its in-process Hub; in multi-replica deployments the catalog handler
// (and others) can land on a replica that doesn't own the WS. Without a
// cross-pod path those calls return "cluster agent not connected" ~half
// the time.
//
// This file exposes POST /internal/tunnel/helm/{cluster_id} which a sibling
// replica's TunnelHelmRequester targets after the redis-backed locator
// reports the WS lives elsewhere. Auth: same PSK scheme as
// /internal/tunnel/k8s/{cluster_id} — DerivePSK over the shared encryption
// key. Body is JSON {msg_type, payload}, response is JSON-encoded
// protocol.HelmResultPayload.
//
// Timeout: helm operations can run 10+ minutes (kube-prom-stack install with
// --wait). The handler honors r.Context — siblings drive the wait via their
// own per-request context, capped at helmTimeout.

package tunnel

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// internalHelmMaxBodyBytes caps the inbound forwarded helm request body.
// HelmRequestPayload carries the values map + chart coords, not large
// content, but kube-prom-stack-class values blobs can be several MiB. 16 MiB
// matches the WS read-limit ceiling so we never accept more than the agent
// can carry.
const internalHelmMaxBodyBytes = 16 << 20

// internalHelmWait caps the wait for the agent's HELM_RESULT once the
// request has been forwarded. Match originators.helmTimeout so a sibling
// proxy doesn't impose a tighter bound than the local path.
const internalHelmWait = 10 * time.Minute

// InternalHelmRequest is the body of POST /internal/tunnel/helm/{cluster_id}.
// We carry MsgType in the body rather than the URL so the endpoint
// surface stays a single route — siblings discriminate per-op only at the
// payload boundary.
type InternalHelmRequest struct {
	MsgType protocol.MessageType        `json:"msg_type"`
	Payload protocol.HelmRequestPayload `json:"payload"`
}

// InternalHelmHandler receives cross-pod helm forwards from sibling
// replicas. Mount it OUTSIDE JWT auth — it carries its own PSK check.
type InternalHelmHandler struct {
	hub *Hub
	psk string
	log *slog.Logger
}

// NewInternalHelmHandler builds the handler. Empty psk 503s every call
// (same disable-by-config behaviour as InternalK8sHandler).
func NewInternalHelmHandler(hub *Hub, psk string, log *slog.Logger) *InternalHelmHandler {
	if log == nil {
		log = slog.Default()
	}
	return &InternalHelmHandler{hub: hub, psk: psk, log: log}
}

// Handle is POST /internal/tunnel/helm/{cluster_id}. The HELM_RESULT
// from the agent is JSON-marshaled back to the caller verbatim.
func (h *InternalHelmHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.psk == "" {
		http.Error(w, `{"error":"internal endpoint disabled"}`, http.StatusServiceUnavailable)
		return
	}
	// Defense in depth: reject anything lacking the sibling-pod source
	// marker even with a valid PSK (see internal_k8s.go). Checked before
	// the PSK so a leaked-PSK attacker over an external route is denied.
	if !hasSiblingSourceSignal(r) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
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

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, internalHelmMaxBodyBytes))
	if err != nil {
		http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
		return
	}
	var req InternalHelmRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, `{"error":"decode payload"}`, http.StatusBadRequest)
		return
	}
	switch req.MsgType {
	case protocol.MsgHelmInstall, protocol.MsgHelmUpgrade,
		protocol.MsgHelmUninstall, protocol.MsgHelmRollback, protocol.MsgHelmStatus:
	default:
		http.Error(w, `{"error":"invalid helm message type"}`, http.StatusBadRequest)
		return
	}

	agent := h.hub.GetAgent(clusterID)
	if agent == nil {
		// Locator pointed at us but the WS dropped between the lookup
		// and now. 503 surfaces the disconnect to the caller, matching
		// InternalK8sHandler's behaviour.
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

	out, mErr := json.Marshal(req.Payload)
	if mErr != nil {
		http.Error(w, `{"error":"`+mErr.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	// roundTrip sets StreamID==RequestID so the agent's response routes
	// correctly via either field. Mirror that here.
	if err := h.hub.SendToAgent(clusterID, &protocol.Message{
		Type:      req.MsgType,
		StreamID:  streamID,
		RequestID: streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   out,
	}); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}

	// Cap at internalHelmWait; the caller's ctx tightens the deadline
	// further when its own per-request budget is shorter.
	waitCtx, cancel := context.WithTimeout(r.Context(), internalHelmWait)
	defer cancel()

	var respBytes []byte
	select {
	case respBytes = <-stream.DataCh:
	case <-stream.DoneCh:
		http.Error(w, `{"error":"stream closed"}`, http.StatusBadGateway)
		return
	case <-waitCtx.Done():
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			http.Error(w, `{"error":"timeout"}`, http.StatusGatewayTimeout)
		} else {
			http.Error(w, `{"error":"`+waitCtx.Err().Error()+`"}`, http.StatusBadGateway)
		}
		return
	}
	var resp protocol.HelmResultPayload
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"decode agent response: %s"}`, err.Error()), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
