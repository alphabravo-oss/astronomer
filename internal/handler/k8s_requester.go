package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// k8sRequesterTracer is the OTel tracer for tunnel k8s requests.
var k8sRequesterTracer = otel.Tracer("astronomer/k8s-requester")

type K8sRequester interface {
	Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error)
}

type TunnelK8sRequester struct {
	hub     *tunnel.Hub
	breaker *clusterBreaker
	// psk authenticates cross-pod calls to the sibling's internal
	// K8sRequest endpoint. Empty disables the fallback (single-replica
	// install) and the requester returns "cluster agent not connected"
	// on local-hub miss, same as before the locator existed.
	psk string
}

func NewTunnelK8sRequester(hub *tunnel.Hub) *TunnelK8sRequester {
	// 5 consecutive failures opens the circuit; 30s cooldown before
	// the half-open trial. Tunable in NewTunnelK8sRequesterWithBreaker
	// for tests + future operator config.
	return NewTunnelK8sRequesterWithBreaker(hub, 5, 30*time.Second)
}

// SetInternalPSK wires the shared-secret PSK that this requester uses
// to authenticate to sibling pods' internal K8sRequest endpoint. Pass
// the same value the InternalK8sHandler is configured with (typically
// tunnel.DerivePSK(cfg.EncryptionKey)). Empty psk leaves the fallback
// disabled.
func (r *TunnelK8sRequester) SetInternalPSK(psk string) {
	if r == nil {
		return
	}
	r.psk = psk
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

	// Span around the full k8s tunnel round-trip. Attributes
	// carry the routed cluster + HTTP method so traces filter on
	// either dimension. retErr is finalized via the named return.
	ctx, span := k8sRequesterTracer.Start(ctx, "tunnel.k8s "+method)
	span.SetAttributes(
		attribute.String("astronomer.cluster_id", clusterID),
		attribute.String("http.request.method", method),
		attribute.String("url.path", path),
	)
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, retErr.Error())
		} else if resp != nil {
			span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
		}
		span.End()
	}()

	// Short-circuit calls to a known-failing
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
		// Cross-pod fallback: ask the locator which sibling pod owns
		// the agent's WS and forward the request there via the internal
		// K8sRequest endpoint. Required for multi-replica server
		// deployments — without this every server-internal tunnel call
		// (shell open SA/Role/Pod create, project reconciler, etc.)
		// 503s for the half of clusters whose WS landed on a sibling.
		if resp, ok, ferr := r.forwardToOwner(ctx, clusterID, method, path, body, headers); ok {
			return resp, ferr
		}
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

	// The agent may respond either with one
	// K8sResponse (small bodies) or with chunked K8sStreamFrame
	// (header + N data + end) for large bodies. Probe the first
	// frame to discriminate; both shapes assemble into a single
	// K8sResponsePayload before returning to the caller.
	first, err := readStreamFrame(ctx, stream.DataCh, stream.DoneCh)
	if err != nil {
		return nil, err
	}
	if isStreamFrame(first) {
		return assembleChunkedResponse(ctx, stream.DataCh, stream.DoneCh, first)
	}
	var parsed protocol.K8sResponsePayload
	if err := json.Unmarshal(first, &parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

// readStreamFrame waits for one frame on the agent stream. Channels are
// passed in directly so this can be exercised by tests with synthetic
// channels (no full Stream construction required).
func readStreamFrame(ctx context.Context, dataCh <-chan []byte, doneCh <-chan struct{}) ([]byte, error) {
	select {
	case data, ok := <-dataCh:
		if !ok {
			return nil, fmt.Errorf("stream closed unexpectedly")
		}
		return data, nil
	case <-doneCh:
		return nil, fmt.Errorf("stream closed unexpectedly")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// isStreamFrame inspects a JSON blob to decide whether it's a
// K8sStreamFrame (has a top-level `kind` field) or a K8sResponsePayload
// (has no `kind`). Cheap one-field probe; we re-unmarshal the full
// struct in the caller.
func isStreamFrame(data []byte) bool {
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.Kind != ""
}

// assembleChunkedResponse reads K8sStreamFrame data frames until it sees
// the end frame, concatenates the base64-decoded body, and returns one
// K8sResponsePayload to the caller — same shape as the single-message
// path. Caps total assembled body at 64 MiB so a runaway agent can't OOM
// the server.
const maxAssembledResponseBytes = 64 * 1024 * 1024

func assembleChunkedResponse(ctx context.Context, dataCh <-chan []byte, doneCh <-chan struct{}, first []byte) (*protocol.K8sResponsePayload, error) {
	var header protocol.K8sStreamFrame
	if err := json.Unmarshal(first, &header); err != nil {
		return nil, fmt.Errorf("decode first stream frame: %w", err)
	}
	if header.Kind != protocol.K8sStreamFrameHeader {
		return nil, fmt.Errorf("expected header frame first, got %q", header.Kind)
	}
	out := &protocol.K8sResponsePayload{
		StatusCode: header.StatusCode,
		Headers:    header.Headers,
	}
	var body bytes.Buffer
	for {
		raw, err := readStreamFrame(ctx, dataCh, doneCh)
		if err != nil {
			return nil, err
		}
		var frame protocol.K8sStreamFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			return nil, fmt.Errorf("decode stream frame: %w", err)
		}
		switch frame.Kind {
		case protocol.K8sStreamFrameData:
			if frame.Body == "" {
				continue
			}
			decoded, derr := base64.StdEncoding.DecodeString(frame.Body)
			if derr != nil {
				return nil, fmt.Errorf("decode chunk body: %w", derr)
			}
			if body.Len()+len(decoded) > maxAssembledResponseBytes {
				return nil, fmt.Errorf("chunked response exceeded %d-byte cap", maxAssembledResponseBytes)
			}
			body.Write(decoded)
		case protocol.K8sStreamFrameEnd:
			if frame.Error != "" {
				return nil, fmt.Errorf("agent reported stream error: %s", frame.Error)
			}
			out.Body = base64.StdEncoding.EncodeToString(body.Bytes())
			return out, nil
		default:
			return nil, fmt.Errorf("unexpected stream frame kind %q during assembly", frame.Kind)
		}
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

// forwardToOwner POSTs the K8sRequest to whichever sibling pod owns the
// cluster's WS, per the redis-backed locator. The `ok` return is false
// when there's no locator, no entry, the locator says we are the owner
// (stale entry — falling through to the 503 surfaces the real
// disconnect), or the PSK isn't configured. retErr non-nil means the
// forward happened but the sibling returned an error.
func (r *TunnelK8sRequester) forwardToOwner(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (resp *protocol.K8sResponsePayload, ok bool, retErr error) {
	if r == nil || r.hub == nil || r.psk == "" {
		return nil, false, nil
	}
	loc := r.hub.Locator()
	if loc == nil {
		return nil, false, nil
	}
	addr, err := loc.Lookup(ctx, clusterID)
	if err != nil || addr == "" || addr == loc.Address() {
		return nil, false, nil
	}

	payload := protocol.K8sRequestPayload{Method: method, Path: path, Headers: headers}
	if len(body) > 0 {
		payload.Body = base64.StdEncoding.EncodeToString(body)
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, true, err
	}
	target := "http://" + addr + "/internal/tunnel/k8s/" + clusterID
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, true, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(tunnel.InternalPSKHeader, r.psk)
	// Defense-in-depth in-band marker proving sibling-pod origin; the
	// receiver rejects requests without it even with a valid PSK.
	req.Header.Set(tunnel.InternalSourceHeader, tunnel.InternalSourceValue)

	httpResp, err := internalK8sForwardClient.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() {
		_ = httpResp.Body.Close()
	}()
	respBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, true, err
	}
	if httpResp.StatusCode >= 400 {
		return nil, true, fmt.Errorf("sibling internal k8s endpoint %d: %s", httpResp.StatusCode, string(respBytes))
	}
	var out protocol.K8sResponsePayload
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return nil, true, fmt.Errorf("decode sibling response: %w", err)
	}
	return &out, true, nil
}

// internalK8sForwardClient is the HTTP client used for cross-pod K8s
// request forwarding. No global timeout — per-request context governs.
var internalK8sForwardClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:          16,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}
