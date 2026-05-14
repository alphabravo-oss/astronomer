// Cross-pod K8s response assembler.
//
// The agent ↔ server tunnel uses two wire shapes for a single K8s
// response: a one-shot K8sResponsePayload for small bodies (so the
// fast path stays one frame) and a chunked K8sStreamFrame sequence
// (header + N data frames + end) for large bodies. The handler-side
// requester (internal/handler/k8s_requester.go) already detects and
// assembles both, but the InternalK8sHandler in this package
// historically only read the FIRST frame and unmarshaled it as a
// K8sResponsePayload — which silently returned status_code + headers
// and a zero-length body whenever the agent picked the chunked path.
//
// That bug surfaced on .247 as the "bravo cluster shows 0 pods"
// regression: half the /clusters/{id}/pods requests landed on the
// non-agent-owning pod, the cross-pod proxy here ate the chunked
// response, and the caller got HTTP 200 with `data: []`.
//
// reassembleK8sResponse keeps the same handle for both shapes.
// Mirrors readStreamFrame + isStreamFrame + assembleChunkedResponse
// from the handler package without exporting those internals.

package tunnel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// maxInternalK8sResponseBytes caps the assembled body for cross-pod
// transport. Matches the handler-side cap so the two paths refuse the
// same payloads.
const maxInternalK8sResponseBytes = 64 * 1024 * 1024

// reassembleK8sResponse reads frames off the agent stream and returns
// a single K8sResponsePayload regardless of whether the agent picked
// the one-shot or chunked encoding.
//
// Returns the canonical "stream closed unexpectedly" / "timeout"
// errors that the caller can translate into HTTP 502 / 504.
func reassembleK8sResponse(ctx context.Context, dataCh <-chan []byte, doneCh <-chan struct{}) (*protocol.K8sResponsePayload, error) {
	first, err := readK8sStreamFrame(ctx, dataCh, doneCh)
	if err != nil {
		return nil, err
	}
	if !isK8sStreamFrame(first) {
		// One-shot: the entire body fit into a single
		// K8sResponsePayload frame.
		var parsed protocol.K8sResponsePayload
		if err := json.Unmarshal(first, &parsed); err != nil {
			return nil, fmt.Errorf("decode agent response: %w", err)
		}
		return &parsed, nil
	}
	// Chunked: drive the header → data… → end state machine.
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
		raw, err := readK8sStreamFrame(ctx, dataCh, doneCh)
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
			if body.Len()+len(decoded) > maxInternalK8sResponseBytes {
				return nil, fmt.Errorf("chunked response exceeded %d-byte cap", maxInternalK8sResponseBytes)
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

func readK8sStreamFrame(ctx context.Context, dataCh <-chan []byte, doneCh <-chan struct{}) ([]byte, error) {
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

// isK8sStreamFrame inspects a JSON blob to discriminate
// K8sStreamFrame (carries a top-level `kind` field) from a one-shot
// K8sResponsePayload (no `kind`). Cheap one-field probe; the caller
// re-unmarshals the full struct.
func isK8sStreamFrame(data []byte) bool {
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.Kind != ""
}
