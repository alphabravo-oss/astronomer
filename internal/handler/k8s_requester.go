package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type K8sRequester interface {
	Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error)
}

type TunnelK8sRequester struct {
	hub     *tunnel.Hub
	breaker *clusterBreaker
}

func NewTunnelK8sRequester(hub *tunnel.Hub) *TunnelK8sRequester {
	// 5 consecutive failures opens the circuit; 30s cooldown before
	// the half-open trial. Tunable in NewTunnelK8sRequesterWithBreaker
	// for tests + future operator config (FEATURES-051126 T19).
	return NewTunnelK8sRequesterWithBreaker(hub, 5, 30*time.Second)
}

// NewTunnelK8sRequesterWithBreaker constructs the requester with explicit
// breaker tunables. Production callers use NewTunnelK8sRequester; tests use
// this to drive the failure thresholds without waiting 30s.
func NewTunnelK8sRequesterWithBreaker(hub *tunnel.Hub, threshold int, openDuration time.Duration) *TunnelK8sRequester {
	return &TunnelK8sRequester{
		hub:     hub,
		breaker: newClusterBreaker(threshold, openDuration),
	}
}

func (r *TunnelK8sRequester) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (resp *protocol.K8sResponsePayload, retErr error) {
	if r == nil || r.hub == nil {
		return nil, fmt.Errorf("tunnel requester not configured")
	}

	// FEATURES-051126 T19: short-circuit calls to a known-failing
	// cluster instead of burning the ctx timeout. The breaker is
	// per-cluster so this only fast-fails the offender; other
	// clusters keep flowing normally. The named-return retErr is
	// captured by the deferred finalize so we record the outcome
	// the function actually returns, regardless of which branch
	// produced it.
	if r.breaker != nil {
		proceed, finalize := r.breaker.allow(clusterID)
		if !proceed {
			return nil, fmt.Errorf("%w for cluster %q", ErrCircuitOpen, clusterID)
		}
		defer func() { finalize(retErr) }()
	}

	agent := r.hub.GetAgent(clusterID)
	if agent == nil {
		return nil, fmt.Errorf("cluster agent not connected")
	}

	streamID := uuid.NewString()
	stream, err := agent.Streams.CreateStream(streamID)
	if err != nil {
		return nil, err
	}
	defer agent.Streams.CloseStream(streamID)

	payload := protocol.K8sRequestPayload{
		Method:  method,
		Path:    path,
		Headers: headers,
	}
	if len(body) > 0 {
		payload.Body = base64.StdEncoding.EncodeToString(body)
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	if err := r.hub.SendToAgent(clusterID, &protocol.Message{
		Type:      protocol.MsgK8sRequest,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   payloadBytes,
	}); err != nil {
		return nil, err
	}

	select {
	case data := <-stream.DataCh:
		var resp protocol.K8sResponsePayload
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		return &resp, nil
	case <-stream.DoneCh:
		return nil, fmt.Errorf("stream closed unexpectedly")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func decodeResponseBody(resp *protocol.K8sResponsePayload) ([]byte, error) {
	if resp == nil || resp.Body == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(resp.Body)
}

func parseJSONResponse(resp *protocol.K8sResponsePayload, out any) error {
	body, err := decodeResponseBody(resp)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func responseError(resp *protocol.K8sResponsePayload) error {
	body, _ := decodeResponseBody(resp)
	if len(body) == 0 {
		return fmt.Errorf("k8s request failed with status %d", resp.StatusCode)
	}
	return fmt.Errorf("k8s request failed with status %d: %s", resp.StatusCode, string(body))
}

func requestHeaders(contentType string) map[string]string {
	headers := map[string]string{
		"Accept": "application/json",
	}
	if contentType != "" {
		headers["Content-Type"] = contentType
	}
	return headers
}

func ensureSuccess(resp *protocol.K8sResponsePayload) error {
	if resp == nil {
		return fmt.Errorf("empty response")
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return responseError(resp)
	}
	return nil
}
