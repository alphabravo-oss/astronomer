package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// HelmReply is the result of a Helm operation that travelled through the tunnel.
type HelmReply = protocol.HelmResultPayload

// RBACReply is the result of an RBAC sync that travelled through the tunnel.
type RBACReply = protocol.RBACSyncResultPayload

// ServiceProxyReply is the result of a service-proxy request through the tunnel.
type ServiceProxyReply = protocol.ServiceProxyResponsePayload

// LogChunk is one line of streamed pod logs as delivered to the API layer.
type LogChunk struct {
	Line string `json:"line"`
	Err  error  `json:"-"`
}

// ExecOutput is one stdout/stderr chunk from an exec session.
type ExecOutput struct {
	Stream string `json:"stream"` // "stdout" / "stderr"
	Data   string `json:"data"`
	End    bool   `json:"-"`
	Err    error  `json:"-"`
}

// LogStream is a handle the API layer uses to consume LOG_DATA frames.
// Lines is closed when the stream ends.
type LogStream struct {
	StreamID string
	Lines    <-chan LogChunk
	Cancel   func()
}

// ExecStream is a handle the API layer uses to consume exec output and to
// pump stdin / resize events.
type ExecStream struct {
	StreamID string
	Output   <-chan ExecOutput
	Cancel   func()

	hub       *Hub
	clusterID string
}

// SendInput pumps a stdin chunk to the agent's exec session.
func (s *ExecStream) SendInput(data []byte) error {
	if s == nil || s.hub == nil {
		return errors.New("exec stream is not active")
	}
	return s.hub.SendToAgent(s.clusterID, &protocol.Message{
		Type:      protocol.MsgExecInput,
		StreamID:  s.StreamID,
		ClusterID: s.clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   json.RawMessage(data),
	})
}

// SendResize delivers a terminal resize event to the agent's exec session.
func (s *ExecStream) SendResize(width, height int) error {
	if s == nil || s.hub == nil {
		return errors.New("exec stream is not active")
	}
	payload, _ := json.Marshal(protocol.ExecResizePayload{Width: width, Height: height})
	return s.hub.SendToAgent(s.clusterID, &protocol.Message{
		Type:      protocol.MsgExecResize,
		StreamID:  s.StreamID,
		ClusterID: s.clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	})
}

// helmTimeout caps a Helm round-trip. Helm operations can be slow (chart
// rendering, hook execution, --wait), so we allow more time than k8s proxy.
const helmTimeout = 10 * time.Minute

// rbacSyncTimeout caps an RBAC sync round-trip.
const rbacSyncTimeout = 60 * time.Second

// serviceProxyTimeout caps a service-proxy round-trip on the server side; the
// agent applies its own per-call timeout independently.
const serviceProxyTimeout = 60 * time.Second

// SendHelmRequest dispatches a HELM_INSTALL / UPGRADE / UNINSTALL / ROLLBACK
// / STATUS message through the tunnel and waits for the matching HELM_RESULT.
//
// The msgType MUST be one of the Helm constants in pkg/protocol.
func (h *Hub) SendHelmRequest(ctx context.Context, clusterID string, msgType protocol.MessageType, payload protocol.HelmRequestPayload) (*HelmReply, error) {
	switch msgType {
	case protocol.MsgHelmInstall, protocol.MsgHelmUpgrade,
		protocol.MsgHelmUninstall, protocol.MsgHelmRollback, protocol.MsgHelmStatus:
	default:
		return nil, fmt.Errorf("invalid helm message type %q", msgType)
	}

	agent := h.GetAgent(clusterID)
	if agent == nil {
		return nil, fmt.Errorf("cluster agent not connected")
	}

	streamID := uuid.NewString()
	stream, err := agent.Streams.CreateStream(streamID)
	if err != nil {
		return nil, err
	}
	defer agent.Streams.CloseStream(streamID)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if err := h.SendToAgent(clusterID, &protocol.Message{
		Type:      msgType,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}); err != nil {
		return nil, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, helmTimeout)
	defer cancel()

	select {
	case data := <-stream.DataCh:
		var result protocol.HelmResultPayload
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		if !result.Success && result.Error != "" {
			return &result, errors.New(result.Error)
		}
		return &result, nil
	case <-stream.DoneCh:
		return nil, errors.New("helm stream closed unexpectedly")
	case <-waitCtx.Done():
		return nil, waitCtx.Err()
	}
}

// SyncRBAC dispatches an RBAC_SYNC_REQUEST and waits for the RBAC_SYNC_RESULT.
func (h *Hub) SyncRBAC(ctx context.Context, clusterID string, payload protocol.RBACSyncRequestPayload) (*RBACReply, error) {
	agent := h.GetAgent(clusterID)
	if agent == nil {
		return nil, fmt.Errorf("cluster agent not connected")
	}
	streamID := uuid.NewString()
	stream, err := agent.Streams.CreateStream(streamID)
	if err != nil {
		return nil, err
	}
	defer agent.Streams.CloseStream(streamID)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if err := h.SendToAgent(clusterID, &protocol.Message{
		Type:      protocol.MsgRBACSyncRequest,
		StreamID:  streamID,
		RequestID: streamID, // agent uses RequestID to address the reply
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}); err != nil {
		return nil, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, rbacSyncTimeout)
	defer cancel()

	select {
	case data := <-stream.DataCh:
		var out protocol.RBACSyncResultPayload
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case <-stream.DoneCh:
		return nil, errors.New("rbac sync stream closed unexpectedly")
	case <-waitCtx.Done():
		return nil, waitCtx.Err()
	}
}

// ServiceProxyRequest dispatches a SERVICE_PROXY_REQUEST and waits for the
// SERVICE_PROXY_RESPONSE.
func (h *Hub) ServiceProxyRequest(ctx context.Context, clusterID string, payload protocol.ServiceProxyRequestPayload) (*ServiceProxyReply, error) {
	agent := h.GetAgent(clusterID)
	if agent == nil {
		return nil, fmt.Errorf("cluster agent not connected")
	}
	streamID := uuid.NewString()
	stream, err := agent.Streams.CreateStream(streamID)
	if err != nil {
		return nil, err
	}
	defer agent.Streams.CloseStream(streamID)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if err := h.SendToAgent(clusterID, &protocol.Message{
		Type:      protocol.MsgServiceProxyRequest,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}); err != nil {
		return nil, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, serviceProxyTimeout)
	defer cancel()

	select {
	case data := <-stream.DataCh:
		var out protocol.ServiceProxyResponsePayload
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case <-stream.DoneCh:
		return nil, errors.New("service proxy stream closed unexpectedly")
	case <-waitCtx.Done():
		return nil, waitCtx.Err()
	}
}

// StartLogStream sends LOG_START and returns a LogStream whose Lines channel
// receives LOG_DATA frames until LOG_END or context cancellation.
//
// The caller MUST invoke LogStream.Cancel when finished to release the agent
// stream slot.
func (h *Hub) StartLogStream(ctx context.Context, clusterID string, payload protocol.LogStartPayload) (*LogStream, error) {
	agent := h.GetAgent(clusterID)
	if agent == nil {
		return nil, fmt.Errorf("cluster agent not connected")
	}

	streamID := uuid.NewString()
	stream, err := agent.Streams.CreateStream(streamID)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		agent.Streams.CloseStream(streamID)
		return nil, err
	}
	if err := h.SendToAgent(clusterID, &protocol.Message{
		Type:      protocol.MsgLogStart,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}); err != nil {
		agent.Streams.CloseStream(streamID)
		return nil, err
	}

	out := make(chan LogChunk, 64)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-stream.DataCh:
				if !ok {
					return
				}
				var line struct {
					Line string `json:"line"`
				}
				if err := json.Unmarshal(data, &line); err != nil {
					select {
					case out <- LogChunk{Err: err}:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case out <- LogChunk{Line: line.Line}:
				case <-ctx.Done():
					return
				}
			case <-stream.DoneCh:
				return
			}
		}
	}()

	cancel := func() {
		agent.Streams.CloseStream(streamID)
	}

	return &LogStream{StreamID: streamID, Lines: out, Cancel: cancel}, nil
}

// StartExecSession sends EXEC_START and returns an ExecStream whose Output
// channel emits EXEC_OUTPUT frames. Pump stdin via ExecStream.SendInput and
// resize events via ExecStream.SendResize.
//
// The caller MUST invoke ExecStream.Cancel when finished.
func (h *Hub) StartExecSession(ctx context.Context, clusterID string, payload protocol.ExecStartPayload) (*ExecStream, error) {
	agent := h.GetAgent(clusterID)
	if agent == nil {
		return nil, fmt.Errorf("cluster agent not connected")
	}

	streamID := uuid.NewString()
	stream, err := agent.Streams.CreateStream(streamID)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		agent.Streams.CloseStream(streamID)
		return nil, err
	}
	if err := h.SendToAgent(clusterID, &protocol.Message{
		Type:      protocol.MsgExecStart,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}); err != nil {
		agent.Streams.CloseStream(streamID)
		return nil, err
	}

	out := make(chan ExecOutput, 64)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-stream.DataCh:
				if !ok {
					return
				}
				// EXEC_OUTPUT and EXEC_END both arrive on the same channel —
				// they share stream_id but currently we don't carry the
				// MessageType down here. The agent sends EXEC_OUTPUT payloads
				// with {stream, data}; EXEC_END payloads with {reason}.
				var probe map[string]any
				if err := json.Unmarshal(data, &probe); err != nil {
					select {
					case out <- ExecOutput{Err: err}:
					case <-ctx.Done():
						return
					}
					continue
				}
				if _, hasReason := probe["reason"]; hasReason {
					select {
					case out <- ExecOutput{End: true}:
					case <-ctx.Done():
					}
					return
				}
				outFrame := ExecOutput{}
				if v, ok := probe["stream"].(string); ok {
					outFrame.Stream = v
				}
				if v, ok := probe["data"].(string); ok {
					outFrame.Data = v
				}
				select {
				case out <- outFrame:
				case <-ctx.Done():
					return
				}
			case <-stream.DoneCh:
				return
			}
		}
	}()

	es := &ExecStream{
		StreamID:  streamID,
		Output:    out,
		hub:       h,
		clusterID: clusterID,
	}
	es.Cancel = func() {
		// Best-effort terminator; the agent also exits when its underlying
		// SPDY stream closes.
		_ = h.SendToAgent(clusterID, &protocol.Message{
			Type:      protocol.MsgExecEnd,
			StreamID:  streamID,
			ClusterID: clusterID,
			Timestamp: time.Now().UTC(),
		})
		agent.Streams.CloseStream(streamID)
	}
	return es, nil
}

// WriteExecInput is a convenience wrapper for callers that already have a
// stream ID (e.g. a long-lived WebSocket relay) but no ExecStream handle.
func (h *Hub) WriteExecInput(clusterID, streamID string, data []byte) error {
	return h.SendToAgent(clusterID, &protocol.Message{
		Type:      protocol.MsgExecInput,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   json.RawMessage(data),
	})
}
