package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// tunnelTracer is the named OTel tracer for span emission in this package.
var tunnelTracer = otel.Tracer("astronomer/tunnel")

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

// roundTrip sends a typed payload to the agent for clusterID and waits
// for the matching reply. The reply is JSON-unmarshaled into Resp.
// Per-call timeout caps the wait; ctx-cancellation is honored.
//
// The outbound message sets both StreamID and RequestID to the same
// generated UUID so agent handlers that address replies by either field
// (e.g. RBAC sync uses RequestID; helm + service-proxy use StreamID)
// route correctly without per-call branching.
//
// Used by SendHelmRequest, SyncRBAC, ServiceProxyRequest — see those
// wrappers for typed entry points + span instrumentation + post-checks.
func roundTrip[Resp any](
	ctx context.Context,
	h *Hub,
	clusterID string,
	msgType protocol.MessageType,
	payload any,
	timeout time.Duration,
) (*Resp, error) {
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
		RequestID: streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}); err != nil {
		return nil, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case data := <-stream.DataCh:
		var result Resp
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		return &result, nil
	case <-stream.DoneCh:
		return nil, errors.New("stream closed unexpectedly")
	case <-waitCtx.Done():
		return nil, waitCtx.Err()
	}
}

// SendHelmRequest dispatches a HELM_INSTALL / UPGRADE / UNINSTALL / ROLLBACK
// / STATUS message through the tunnel and waits for the matching HELM_RESULT.
//
// The msgType MUST be one of the Helm constants in pkg/protocol.
func (h *Hub) SendHelmRequest(ctx context.Context, clusterID string, msgType protocol.MessageType, payload protocol.HelmRequestPayload) (reply *HelmReply, err error) {
	// Span around the full helm round-trip. Named after the msg
	// type so traces show "helm.HELM_INSTALL" / "helm.HELM_UPGRADE"
	// etc. Attributes carry cluster ID + release name for filtering.
	ctx, span := tunnelTracer.Start(ctx, "tunnel.helm "+string(msgType))
	span.SetAttributes(
		attribute.String("astronomer.cluster_id", clusterID),
		attribute.String("astronomer.helm.release", payload.ReleaseName),
		attribute.String("astronomer.helm.namespace", payload.Namespace),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	switch msgType {
	case protocol.MsgHelmInstall, protocol.MsgHelmUpgrade,
		protocol.MsgHelmUninstall, protocol.MsgHelmRollback, protocol.MsgHelmStatus:
	default:
		return nil, fmt.Errorf("invalid helm message type %q", msgType)
	}

	reply, err = roundTrip[protocol.HelmResultPayload](ctx, h, clusterID, msgType, payload, helmTimeout)
	if err != nil {
		return reply, err
	}
	if !reply.Success && reply.Error != "" {
		return reply, errors.New(reply.Error)
	}
	return reply, nil
}

// SyncRBAC dispatches an RBAC_SYNC_REQUEST and waits for the RBAC_SYNC_RESULT.
//
// The agent replies with RequestID only (no StreamID); roundTrip sets both
// fields on the outbound message so the server's stream router picks up
// the reply via its RequestID fallback.
func (h *Hub) SyncRBAC(ctx context.Context, clusterID string, payload protocol.RBACSyncRequestPayload) (*RBACReply, error) {
	return roundTrip[protocol.RBACSyncResultPayload](ctx, h, clusterID, protocol.MsgRBACSyncRequest, payload, rbacSyncTimeout)
}

// ServiceProxyRequest dispatches a SERVICE_PROXY_REQUEST and waits for the
// SERVICE_PROXY_RESPONSE.
func (h *Hub) ServiceProxyRequest(ctx context.Context, clusterID string, payload protocol.ServiceProxyRequestPayload) (*ServiceProxyReply, error) {
	return roundTrip[protocol.ServiceProxyResponsePayload](ctx, h, clusterID, protocol.MsgServiceProxyRequest, payload, serviceProxyTimeout)
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
